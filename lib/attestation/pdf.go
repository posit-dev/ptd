package attestation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// Color palette
var (
	headerBg    = &props.Color{Red: 240, Green: 240, Blue: 245}
	accentColor = &props.Color{Red: 60, Green: 90, Blue: 150}
	mutedColor  = &props.Color{Red: 120, Green: 120, Blue: 120}
	borderColor = &props.Color{Red: 200, Green: 200, Blue: 200}
)

// RenderPDF generates a PDF attestation document and writes it to the given path.
func RenderPDF(outputPath string, data *AttestationData) error {
	sort.Slice(data.Stacks, func(i, j int) bool {
		return stackOrder(data.Stacks[i].ProjectName) < stackOrder(data.Stacks[j].ProjectName)
	})

	cfg := config.NewBuilder().
		WithPageNumber(props.PageNumber{
			Pattern: "Page {current} of {total}",
			Place:   props.RightBottom,
			Size:    8,
			Color:   mutedColor,
		}).
		WithLeftMargin(18).
		WithTopMargin(18).
		WithRightMargin(18).
		Build()

	m := maroto.New(cfg)

	// Title
	m.AddRows(
		text.NewRow(16, fmt.Sprintf("Installation Attestation — %s", data.TargetName), props.Text{
			Size:  20,
			Style: fontstyle.Bold,
			Align: align.Left,
			Color: accentColor,
		}),
	)
	m.AddRows(line.NewRow(2, props.Line{
		Color:     accentColor,
		Thickness: 1.5,
	}))
	m.AddRows(row.New(4))

	// Metadata
	pdfMetaRow(m, "Environment", data.TargetName)
	pdfMetaRow(m, "AWS Account", data.AccountID)
	pdfMetaRow(m, "Region", data.Region)
	for _, site := range data.Sites {
		pdfMetaRow(m, "Site Domain", site.Domain)
	}
	pdfMetaRow(m, "Date", data.GeneratedAt.Format("2006-01-02"))

	m.AddRows(row.New(6))

	// Purpose
	pdfSection(m, "Purpose")
	pdfParagraph(m, "This document attests that the Posit Team Dedicated (PTD) platform has been installed and configured in the target AWS account as specified in the declarative configuration files maintained in version control. It provides a summary of all infrastructure and application resources provisioned, the product versions deployed, and references to the Pulumi state files that serve as the authoritative record of the installation.")

	m.AddRows(row.New(4))

	// Sites and products
	for _, site := range data.Sites {
		pdfSection(m, fmt.Sprintf("Installed Products — %s", site.SiteName))

		m.AddRows(pdfTableHeader([]string{"Product", "Version", "Domain"}, []int{4, 3, 5}))
		for _, product := range site.Products {
			domain := "—"
			if product.DomainPrefix != "" {
				domain = product.DomainPrefix + "." + site.Domain
			}
			m.AddRows(pdfTableRow([]string{productDisplayName(product.Name), product.Version, domain}, []int{4, 3, 5}))
		}

		m.AddRows(row.New(4))

		// Per-product auth
		hasAuth := false
		for _, p := range site.Products {
			if p.Auth != nil {
				hasAuth = true
				break
			}
		}
		if hasAuth {
			pdfSubSection(m, "Authentication Configuration")
			m.AddRows(pdfTableHeader([]string{"Product", "Method", "Identity Provider"}, []int{4, 3, 5}))
			for _, p := range site.Products {
				if p.Auth != nil {
					m.AddRows(pdfTableRow([]string{productDisplayName(p.Name), p.Auth.Type, p.Auth.Issuer}, []int{4, 3, 5}))
				}
			}
			m.AddRows(row.New(4))
		}
	}

	// Product summary
	if data.ProductSummary != "" {
		pdfParagraph(m, data.ProductSummary)
		m.AddRows(row.New(4))
	}

	// Infrastructure summary
	pdfSection(m, "Infrastructure Summary")
	pdfParagraph(m, "PTD provisions infrastructure through a series of ordered Pulumi stacks, each managing a distinct layer of the deployment. All stacks use the Pulumi self-managed backend with state stored in S3 and secrets encrypted via AWS KMS.")

	m.AddRows(row.New(3))

	totalResources := 0
	for _, s := range data.Stacks {
		totalResources += s.ResourceCount
	}
	pdfMetaRow(m, "State backend", fmt.Sprintf("s3://ptd-%s/.pulumi/stacks/", data.TargetName))
	pdfMetaRow(m, "Encryption", fmt.Sprintf("AWS KMS key alias/posit-team-dedicated in account %s", data.AccountID))

	m.AddRows(row.New(4))
	pdfSubSection(m, "Stack Overview")

	// Stack table
	m.AddRows(pdfTableHeader([]string{"Stack", "Purpose", "Resources"}, []int{3, 7, 2}))
	for _, stack := range data.Stacks {
		m.AddRows(pdfTableRow([]string{
			stack.ProjectName,
			stack.Purpose,
			fmt.Sprintf("%d", stack.ResourceCount),
		}, []int{3, 7, 2}))
	}

	m.AddRows(row.New(2))
	m.AddRows(
		text.NewRow(7, fmt.Sprintf("Total managed resources: %d", totalResources), props.Text{
			Size:  9,
			Style: fontstyle.Bold,
			Align: align.Left,
		}),
	)

	m.AddRows(row.New(4))

	// Stack details with prose
	pdfSubSection(m, "Stack Details")
	for _, stack := range data.Stacks {
		stepName := stack.stepNameFromProject()
		prose := GenerateStackProse(stepName, data.Infra)
		if prose == "" && stack.Purpose != "" {
			prose = stack.Purpose
		}
		if prose != "" {
			pdfStackDetail(m, stack.ProjectName, prose)
		}
	}

	m.AddRows(row.New(2))

	// Custom steps
	if len(data.CustomSteps) > 0 {
		pdfSubSection(m, "Custom Steps")
		for _, step := range data.CustomSteps {
			if step.Enabled {
				pdfBullet(m, fmt.Sprintf("%s (inserted after %s%s): %s", step.Name, step.InsertAfter, step.InsertBefore, step.Description))
			}
		}
		m.AddRows(row.New(4))
	}

	// Verification
	pdfSection(m, "Verification")
	pdfSubSection(m, "Pulumi State Files")
	pdfParagraph(m, "The authoritative proof of installation is the set of Pulumi state files stored in the workload's S3 bucket. Each state file contains a complete inventory of every resource managed by that stack, including resource types, cloud provider IDs, configuration values, and timestamps.")

	m.AddRows(row.New(3))

	pdfSubSection(m, "Expected State Files")
	m.AddRows(pdfTableHeader([]string{"File Path", "Stack"}, []int{7, 5}))
	for _, stack := range data.Stacks {
		m.AddRows(pdfTableRow([]string{stack.S3Key, stack.ProjectName}, []int{7, 5}))
	}

	m.AddRows(row.New(4))
	pdfParagraph(m, "Sensitive values (database passwords, API keys, TLS private keys) are encrypted at rest using the AWS KMS key alias/posit-team-dedicated in the target account.")

	// Tools
	m.AddRows(row.New(4))
	pdfSection(m, "Tools")
	pdfBullet(m, "ptd CLI — github.com/posit-dev/ptd — Open-source infrastructure tool that reads configuration files and converges the target AWS account to the declared state.")
	pdfBullet(m, "Pulumi — Infrastructure-as-code engine used by PTD to manage cloud resources declaratively. State is stored in S3 with KMS encryption.")
	pdfBullet(m, "Team Operator — Kubernetes operator (github.com/posit-dev/team-operator) that manages the lifecycle of Posit products within the cluster.")

	// Sign-off
	pdfSection(m, "Sign-Off")
	m.AddRows(pdfTableHeader([]string{"", "Name", "Date"}, []int{3, 5, 4}))
	for _, role := range []string{"Prepared By", "Approved By"} {
		m.AddRows(pdfSignatureRow(role))
	}

	// Generate
	doc, err := m.Generate()
	if err != nil {
		return fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.Save(outputPath)
}

// pdfSection renders a section header with an accent-colored underline.
func pdfSection(m core.Maroto, title string) {
	m.AddRows(row.New(3))
	m.AddRows(
		text.NewRow(10, title, props.Text{
			Size:  14,
			Style: fontstyle.Bold,
			Align: align.Left,
			Color: accentColor,
		}),
	)
	m.AddRows(line.NewRow(2, props.Line{
		Color:     borderColor,
		Thickness: 0.5,
	}))
	m.AddRows(row.New(2))
}

// pdfSubSection renders a sub-section header.
func pdfSubSection(m core.Maroto, title string) {
	m.AddRows(
		text.NewRow(8, title, props.Text{
			Size:  11,
			Style: fontstyle.Bold,
			Align: align.Left,
		}),
	)
	m.AddRows(row.New(1))
}

// pdfMetaRow renders a label: value metadata pair.
func pdfMetaRow(m core.Maroto, label string, value string) {
	m.AddRows(
		row.New(6).Add(
			col.New(3).Add(text.New(label+":", props.Text{
				Size:  9,
				Style: fontstyle.Bold,
				Align: align.Left,
				Color: mutedColor,
			})),
			col.New(9).Add(text.New(value, props.Text{
				Size:  9,
				Align: align.Left,
			})),
		),
	)
}

// pdfParagraph renders a paragraph of text.
func pdfParagraph(m core.Maroto, content string) {
	m.AddRows(
		text.NewRow(12, content, props.Text{
			Size:  9,
			Align: align.Left,
		}),
	)
}

// pdfBullet renders a single bullet point.
func pdfBullet(m core.Maroto, content string) {
	m.AddRows(
		row.New(6).Add(
			col.New(1).Add(text.New("•", props.Text{
				Size:  9,
				Align: align.Center,
			})),
			col.New(11).Add(text.New(content, props.Text{
				Size:  8,
				Align: align.Left,
			})),
		),
	)
}

// pdfStackDetail renders a stack name and its prose description.
func pdfStackDetail(m core.Maroto, name string, prose string) {
	m.AddRows(
		text.NewRow(7, name, props.Text{
			Size:  10,
			Style: fontstyle.BoldItalic,
			Align: align.Left,
		}),
	)

	lines := strings.Split(prose, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "- ") {
			pdfBullet(m, l[2:])
		} else {
			m.AddRows(
				text.NewRow(6, l, props.Text{
					Size:  9,
					Align: align.Left,
				}),
			)
		}
	}
	m.AddRows(row.New(2))
}

// pdfTableHeader renders a shaded table header row.
func pdfTableHeader(headers []string, sizes []int) core.Row {
	cols := make([]core.Col, len(headers))
	for i, h := range headers {
		cols[i] = col.New(sizes[i]).Add(text.New(h, props.Text{
			Size:  8,
			Style: fontstyle.Bold,
			Align: align.Left,
		}))
	}
	return row.New(6).Add(cols...).WithStyle(&props.Cell{
		BackgroundColor: headerBg,
		BorderType:      border.Bottom,
		BorderColor:     borderColor,
		BorderThickness: 0.3,
	})
}

// pdfTableRow renders a table data row with a subtle bottom border.
func pdfTableRow(values []string, sizes []int) core.Row {
	cols := make([]core.Col, len(values))
	for i, v := range values {
		cols[i] = col.New(sizes[i]).Add(text.New(v, props.Text{
			Size:  8,
			Align: align.Left,
		}))
	}
	return row.New(6).Add(cols...).WithStyle(&props.Cell{
		BorderType:      border.Bottom,
		BorderColor:     borderColor,
		BorderThickness: 0.2,
	})
}

// pdfSignatureRow renders a signature line row with the role label and blank fields.
func pdfSignatureRow(role string) core.Row {
	return row.New(12).Add(
		col.New(3).Add(text.New(role, props.Text{
			Size:  9,
			Style: fontstyle.Bold,
			Align: align.Left,
		})),
		col.New(5).Add(text.New("", props.Text{Size: 9})),
		col.New(4).Add(text.New("", props.Text{Size: 9})),
	).WithStyle(&props.Cell{
		BorderType:      border.Bottom,
		BorderColor:     borderColor,
		BorderThickness: 0.3,
	})
}

func productDisplayName(name string) string {
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
}
