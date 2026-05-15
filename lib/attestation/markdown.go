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
	"upper":       strings.ToUpper,
	"productName": ProductDisplayName,
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
		stepName := stack.StepNameFromProject()
		return GenerateStackProse(stepName, infra)
	},
	"purposeText":      func(cloud string) string { return purposeTextFor(cloud) },
	"infraSummaryText": func(cloud string) string { return infraSummaryTextFor(cloud) },
	"verificationText": func(cloud string) string { return verificationTextFor(cloud) },
	"encryptionText":   func(cloud string) string { return encryptionTextFor(cloud) },
	"accountLabel":     AccountLabel,
	"stateBackend": func(data *AttestationData) string {
		if data.StateBackendURL != "" {
			return data.StateBackendURL
		}
		// fallback for tests/older code paths
		if data.Infra != nil && data.Infra.Cloud == "azure" {
			return fmt.Sprintf("azblob://<container>?storage_account=%s", data.TargetName)
		}
		return fmt.Sprintf("s3://ptd-%s/.pulumi/stacks/", data.TargetName)
	},
	"encryptionBackend": func(data *AttestationData) string {
		if data.Infra != nil && data.Infra.Cloud == "azure" {
			return fmt.Sprintf("Azure Key Vault `posit-team-dedicated` in subscription `%s`", data.AccountID)
		}
		return fmt.Sprintf("AWS KMS key `alias/posit-team-dedicated` in account `%s`", data.AccountID)
	},
	"cloudName": func(infra *InfraConfig) string {
		if infra != nil && infra.Cloud == "azure" {
			return "azure"
		}
		return "aws"
	},
}

var markdownTemplate = template.Must(template.New("attestation").Funcs(funcMap).Parse(
	`{{- $cloud := cloudName .Infra -}}
# {{ .DisplayTitle }}

**Environment:** {{ .TargetName }}
**{{ accountLabel $cloud }}:** {{ .AccountID }}
**Region:** {{ .Region }}
{{- range .Sites }}
**Site Domain:** {{ .Domain }}
{{- end }}
**Date:** {{ .GeneratedAt | formatDate }}

## Purpose

{{ purposeText $cloud }}

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

{{ infraSummaryText $cloud }}

**State backend:** ` + "`" + `{{ stateBackend . }}` + "`" + `
**Encryption:** {{ encryptionBackend . }}

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

{{ verificationText $cloud }}

**Location:** ` + "`" + `{{ stateBackend . }}` + "`" + `

Each state file is a JSON document with the following structure:

- ` + "`" + `checkpoint.latest.manifest` + "`" + ` — Timestamp of last successful deployment and Pulumi engine version
- ` + "`" + `checkpoint.latest.resources` + "`" + ` — Complete list of managed resources with types, provider IDs, inputs, and outputs
- ` + "`" + `checkpoint.latest.secrets_providers` + "`" + ` — Encryption configuration

{{ encryptionText $cloud }} These values appear as ciphertext in the state files and can only be decrypted by principals with access to the encryption key.

### Expected State Files

| File Path | Stack |
|---|---|
{{ range .Stacks -}}
| ` + "`" + `{{ .StateKey }}` + "`" + ` | {{ .ProjectName }} |
{{ end }}

### Configuration Source

The declarative configuration files that define this environment are maintained in a private, version-controlled Git repository. The primary files are:

- **` + "`" + `ptd.yaml` + "`" + `** — Infrastructure configuration
{{ range .Sites -}}
- **` + "`" + `site_{{ .SiteName }}/site.yaml` + "`" + `** — Product versions, authentication, and application settings{{ if gt (len $.Sites) 1 }} for {{ .SiteName }}{{ end }}
{{ end }}
The Git history for the ` + "`" + `{{ .TargetName }}/` + "`" + ` directory provides a complete audit trail of every configuration change made to this environment.

## Tools

- **` + "`" + `ptd` + "`" + ` CLI** — [github.com/posit-dev/ptd](https://github.com/posit-dev/ptd) — Open-source infrastructure tool that reads the configuration files and converges the target to the declared state. The primary command is ` + "`" + `ptd ensure` + "`" + `.
- **Pulumi** — Infrastructure-as-code engine used by PTD to manage cloud resources declaratively.
- **Team Operator** — Kubernetes operator ([github.com/posit-dev/team-operator](https://github.com/posit-dev/team-operator)) that manages the lifecycle of Posit products within the cluster.

## Confirmation

` + confirmationText + `

_Generated: {{ .GeneratedAt | formatDate }}_

## Sign-Off

| | Name | Date |
|---|---|---|
| Prepared By | | {{ .GeneratedAt | formatDate }} |
`))

// RenderMarkdown writes the attestation data as a Markdown document to the given writer.
func RenderMarkdown(w io.Writer, data *AttestationData) error {
	sort.Slice(data.Stacks, func(i, j int) bool {
		return StackOrder(data.Stacks[i].ProjectName) < StackOrder(data.Stacks[j].ProjectName)
	})

	return markdownTemplate.Execute(w, data)
}

// StackOrder returns a sort key for known stack names to present them in deployment order.
func StackOrder(name string) int {
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
