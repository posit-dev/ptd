package attestation

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"
	"time"
)

var funcMap = template.FuncMap{
	"formatDate": func(t time.Time) string {
		return t.Format("2006-01-02")
	},
	"upper": strings.ToUpper,
	"productName": func(name string) string {
		switch name {
		case "connect":
			return "Posit Connect"
		case "workbench":
			return "Posit Workbench"
		case "package-manager":
			return "Posit Package Manager"
		case "chronicle":
			return "Chronicle"
		case "chronicle-agent":
			return "Chronicle Agent"
		default:
			return name
		}
	},
	"totalResources": func(stacks []StackSummary) int {
		total := 0
		for _, s := range stacks {
			total += s.ResourceCount
		}
		return total
	},
	"domain": func(prefix string, domain string) string {
		if prefix == "" {
			return "—"
		}
		return prefix + "." + domain
	},
	"hasAuth": func(products []ProductInfo) bool {
		for _, p := range products {
			if p.Auth != nil {
				return true
			}
		}
		return false
	},
	"stackProse": func(stack StackSummary, infra *InfraConfig) string {
		stepName := stack.stepNameFromProject()
		return GenerateStackProse(stepName, infra)
	},
}

var markdownTemplate = template.Must(template.New("attestation").Funcs(funcMap).Parse(markdownTmpl))

const markdownTmpl = `# Installation Attestation — {{ .TargetName }}

**Environment:** {{ .TargetName }}
**AWS Account:** {{ .AccountID }}
**Region:** {{ .Region }}
{{- range .Sites }}
**Site Domain:** {{ .Domain }}
{{- end }}
**Date:** {{ .GeneratedAt | formatDate }}

## Purpose

This document attests that the Posit Team Dedicated (PTD) platform has been installed and configured in the target AWS account as specified in the declarative configuration files maintained in version control. It provides a summary of all infrastructure and application resources provisioned, the product versions deployed, and references to the Pulumi state files that serve as the authoritative record of the installation.

## Installed Products

The following Posit products are deployed and operational at the listed versions:
{{ range .Sites }}
{{ if gt (len $.Sites) 1 -}}
### {{ .SiteName }} ({{ .Domain }})
{{ end }}
| Product | Version | Domain |
|---|---|---|
{{ $siteDomain := .Domain -}}
{{ range .Products -}}
| {{ .Name | productName }} | {{ .Version }} | {{ domain .DomainPrefix $siteDomain }} |
{{ end }}
{{ end -}}

{{ .ProductSummary }}

## Authentication Configuration
{{ range .Sites }}
{{ if gt (len $.Sites) 1 -}}
### {{ .SiteName }}
{{ end }}
{{ if .Products | hasAuth -}}
| Product | Method | Identity Provider |
|---|---|---|
{{ range .Products -}}
{{ if .Auth -}}
| {{ .Name | productName }} | {{ .Auth.Type }} | ` + "`" + `{{ .Auth.Issuer }}` + "`" + ` |
{{ end -}}
{{ end }}
{{ end -}}
{{ end }}

## Infrastructure Summary

PTD provisions infrastructure through a series of ordered Pulumi stacks, each managing a distinct layer of the deployment. All stacks use the Pulumi self-managed backend with state stored in S3 and secrets encrypted via AWS KMS.

**State backend:** ` + "`" + `s3://ptd-{{ .TargetName }}/.pulumi/stacks/` + "`" + `
**Encryption:** AWS KMS key ` + "`" + `alias/posit-team-dedicated` + "`" + ` in account ` + "`" + `{{ .AccountID }}` + "`" + `

### Stack Overview

The following table summarizes each Pulumi stack, its purpose, and the number of managed resources.

| Stack | Purpose | Resources |
|---|---|---|
{{ range .Stacks -}}
| ` + "`" + `{{ .ProjectName }}` + "`" + ` | {{ .Purpose }} | {{ .ResourceCount }} |
{{ end }}
**Total managed resources: {{ totalResources .Stacks }}**

Resource counts are exact as reported by the Pulumi state files at the time of generation.

### Stack Details
{{ range .Stacks }}
#### {{ .ProjectName }}
{{ $prose := stackProse . $.Infra -}}
{{ if $prose }}
{{ $prose }}
{{ else if .Purpose }}
{{ .Purpose }}
{{ end -}}
{{ end }}
{{- if .CustomSteps }}
### Custom Steps

The following custom infrastructure steps are deployed in addition to the standard PTD pipeline:
{{ range .CustomSteps -}}
{{ if .Enabled }}
**{{ .Name }}** (inserted after {{ .InsertAfter }}{{ .InsertBefore }}): {{ .Description }}
{{ end -}}
{{ end }}
{{ end -}}

## Verification

### Pulumi State Files

The authoritative proof of installation is the set of Pulumi state files stored in the workload's S3 bucket. Each state file contains a complete inventory of every resource managed by that stack, including resource types, cloud provider IDs, configuration values, and timestamps.

**Location:** ` + "`" + `s3://ptd-{{ .TargetName }}/.pulumi/stacks/` + "`" + `

To retrieve the current state files:

` + "```" + `bash
{{ if .Profile -}}
export AWS_PROFILE={{ .Profile }}
{{ end -}}
aws s3 ls s3://ptd-{{ .TargetName }}/.pulumi/stacks/ --recursive
` + "```" + `

To download all state files for review:

` + "```" + `bash
aws s3 cp s3://ptd-{{ .TargetName }}/.pulumi/stacks/ ./state-files/ \
  --recursive --exclude "*.bak"
` + "```" + `

Each state file is a JSON document with the following structure:

- ` + "`" + `checkpoint.latest.manifest` + "`" + ` — Timestamp of last successful deployment and Pulumi engine version
- ` + "`" + `checkpoint.latest.resources` + "`" + ` — Complete list of managed resources with types, provider IDs, inputs, and outputs
- ` + "`" + `checkpoint.latest.secrets_providers` + "`" + ` — Encryption configuration (AWS KMS)

Sensitive values (database passwords, API keys, TLS private keys) are encrypted at rest using the AWS KMS key ` + "`" + `alias/posit-team-dedicated` + "`" + ` in the target account. These values appear as ciphertext in the state files and can only be decrypted by principals with access to the KMS key.

### Expected State Files

| File Path | Stack |
|---|---|
{{ range .Stacks -}}
| ` + "`" + `{{ .S3Key }}` + "`" + ` | {{ .ProjectName }} |
{{ end }}

### Configuration Source

The declarative configuration files that define this environment are maintained in a private, version-controlled Git repository. The primary files are:

- **` + "`" + `ptd.yaml` + "`" + `** — Infrastructure configuration (VPC, cluster, DNS, IAM, networking)
{{ range .Sites -}}
- **` + "`" + `site_{{ .SiteName }}/site.yaml` + "`" + `** — Product versions, authentication, and application settings{{ if gt (len $.Sites) 1 }} for {{ .SiteName }}{{ end }}
{{ end }}
The Git history for the ` + "`" + `{{ .TargetName }}/` + "`" + ` directory provides a complete audit trail of every configuration change made to this environment.

## Tools

- **` + "`" + `ptd` + "`" + ` CLI** — [github.com/posit-dev/ptd](https://github.com/posit-dev/ptd) — Open-source infrastructure tool that reads the configuration files and converges the target AWS account to the declared state. The primary command is ` + "`" + `ptd ensure` + "`" + `.
- **Pulumi** — Infrastructure-as-code engine used by PTD to manage cloud resources declaratively. State is stored in S3 with KMS encryption.
- **Team Operator** — Kubernetes operator ([github.com/posit-dev/team-operator](https://github.com/posit-dev/team-operator)) that manages the lifecycle of Posit products within the cluster.

## Sign-Off

| | Name | Date |
|---|---|---|
| Prepared By | | |
| Approved By | | |
`

// RenderMarkdown writes the attestation data as a Markdown document to the given writer.
func RenderMarkdown(w io.Writer, data *AttestationData) error {
	sort.Slice(data.Stacks, func(i, j int) bool {
		return stackOrder(data.Stacks[i].ProjectName) < stackOrder(data.Stacks[j].ProjectName)
	})

	return markdownTemplate.Execute(w, data)
}

// stackOrder returns a sort key for known stack names to present them in deployment order
func stackOrder(name string) int {
	order := map[string]int{
		"persistent":      1,
		"postgres-config": 2,
		"eks":             3,
		"aks":             3,
		"clusters":        5,
		"helm":            7,
		"sites":           8,
	}

	for suffix, idx := range order {
		if strings.HasSuffix(name, suffix) {
			return idx
		}
	}
	return 4
}

// RenderMarkdownString returns the attestation as a Markdown string.
func RenderMarkdownString(data *AttestationData) (string, error) {
	var buf strings.Builder
	if err := RenderMarkdown(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render markdown: %w", err)
	}
	return buf.String(), nil
}
