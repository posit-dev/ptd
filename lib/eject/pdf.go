package eject

import (
	"fmt"
	"strings"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/breakline"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
	att "github.com/posit-dev/ptd/lib/attestation"
)

// RenderHandoffPDF generates the eject handoff PDF and writes it to the given path.
func RenderHandoffPDF(outputPath string, data *HandoffData) error {
	cfg := config.NewBuilder().
		WithPageNumber(props.PageNumber{
			Pattern: "Page {current} of {total}",
			Place:   props.RightBottom,
			Size:    8,
			Color:   att.MutedColor,
		}).
		WithLeftMargin(18).
		WithTopMargin(18).
		WithRightMargin(18).
		Build()

	m := maroto.New(cfg)

	cloud := "aws"
	if data.Infra != nil && data.Infra.Cloud != "" {
		cloud = data.Infra.Cloud
	}

	// Title
	m.AddRows(
		text.NewRow(16, fmt.Sprintf("%s — %s", ejectDocTitle, data.TargetName), props.Text{
			Size:  20,
			Style: fontstyle.Bold,
			Align: align.Left,
			Color: att.AccentColor,
		}),
	)
	m.AddRows(line.NewRow(2, props.Line{
		Color:     att.AccentColor,
		Thickness: 1.5,
	}))
	m.AddRows(row.New(4))

	// Metadata
	att.PdfMetaRow(m, "Environment", data.TargetName)
	att.PdfMetaRow(m, att.AccountLabel(cloud), data.AccountID)
	att.PdfMetaRow(m, "Region", data.Region)
	for _, site := range data.Sites {
		att.PdfMetaRow(m, "Site Domain", site.Domain)
	}
	att.PdfMetaRow(m, "Date", data.GeneratedAt.Format("2006-01-02"))
	if data.PTDVersion != "" {
		att.PdfMetaRow(m, "PTD Version", data.PTDVersion)
	}
	m.AddRows(row.New(6))

	// Infrastructure Overview
	att.PdfSection(m, "Infrastructure Overview")
	att.PdfParagraph(m, overviewText(cloud))
	m.AddRows(row.New(4))

	// Taking Ownership
	att.PdfSection(m, "Taking Ownership")
	pdfMultilineParagraph(m, ownershipOptionsText())
	m.AddRows(row.New(4))

	// Pulumi State Ownership
	att.PdfSection(m, "Pulumi State Ownership")
	pdfMultilineParagraph(m, pulumiOwnershipText(cloud, data.TargetName))
	m.AddRows(row.New(3))

	att.PdfSubSection(m, "State Files")
	m.AddRows(wrappingTableHeader([]string{"File Path", "Stack"}, []int{7, 5}))
	for _, stack := range data.Stacks {
		m.AddRows(wrappingTableRow([]string{stack.StateKey, stack.ProjectName}, []int{7, 5}))
	}
	m.AddRows(row.New(4))

	att.PdfSubSection(m, "Generating a Standalone Pulumi Program")
	pdfMultilineParagraph(m, pulumiImportText())
	m.AddRows(row.New(4))

	att.PdfSubSection(m, "Migrating to Terraform")
	pdfMultilineParagraph(m, terraformMigrationText())
	m.AddRows(row.New(4))

	// Continuing with the PTD CLI
	att.PdfSubSection(m, "Continuing with the PTD CLI")
	att.PdfParagraph(m, "The PTD CLI (https://github.com/posit-dev/ptd) reads the ptd.yaml and site.yaml configuration files included in this bundle and converges infrastructure to match the declared state. Use --help on any command to see available options and usage details. Each infrastructure layer corresponds to a Pulumi step that can be run independently:")
	m.AddRows(row.New(3))

	m.AddRows(wrappingTableHeader([]string{"Command", "Description"}, []int{5, 7}))
	for _, stack := range data.Stacks {
		stepName := stack.StepNameFromProject()
		cmd := fmt.Sprintf("ptd ensure %s --only-steps %s", data.TargetName, stepName)
		desc := ptdCommandDescription(stepName)
		m.AddRows(wrappingTableRow([]string{cmd, desc}, []int{5, 7}))
	}
	m.AddRows(row.New(4))

	// Installed Products
	for _, site := range data.Sites {
		att.PdfSection(m, fmt.Sprintf("Installed Products — %s", site.SiteName))

		m.AddRows(wrappingTableHeader([]string{"Product", "Version", "Domain"}, []int{4, 3, 5}))
		for _, product := range site.Products {
			domain := "—"
			if product.DomainPrefix != "" {
				domain = product.DomainPrefix + "." + site.Domain
			}
			m.AddRows(wrappingTableRow([]string{att.ProductDisplayName(product.Name), product.Version, domain}, []int{4, 3, 5}))
		}
		m.AddRows(row.New(4))

		hasAuth := false
		for _, p := range site.Products {
			if p.Auth != nil {
				hasAuth = true
				break
			}
		}
		if hasAuth {
			att.PdfSubSection(m, "Authentication Configuration")
			m.AddRows(wrappingTableHeader([]string{"Product", "Method", "Identity Provider"}, []int{4, 3, 5}))
			for _, p := range site.Products {
				if p.Auth != nil {
					m.AddRows(wrappingTableRow([]string{att.ProductDisplayName(p.Name), p.Auth.Type, p.Auth.Issuer}, []int{4, 3, 5}))
				}
			}
			m.AddRows(row.New(4))
		}
	}

	if data.ProductSummary != "" {
		att.PdfParagraph(m, data.ProductSummary)
		m.AddRows(row.New(4))
	}

	// Stack Details
	att.PdfSection(m, "Stack Details")
	for _, stack := range data.Stacks {
		stepName := stack.StepNameFromProject()
		prose := att.GenerateStackProse(stepName, data.Infra)
		if prose == "" && stack.Purpose != "" {
			prose = stack.Purpose
		}
		if prose != "" {
			att.PdfStackDetail(m, stack.ProjectName, prose)
		}
	}

	// Secret References
	if len(data.Secrets) > 0 {
		att.PdfSection(m, "Secret References")
		att.PdfParagraph(m, "The following secrets are managed in your cloud provider's secret store. Values are not included in this bundle — access them directly via AWS Secrets Manager or Azure Key Vault using the names below.")
		m.AddRows(row.New(3))

		for _, secret := range data.Secrets {
			att.PdfSubSection(m, secret.Name)
			att.PdfParagraph(m, fmt.Sprintf("Purpose: %s. Created by: %s.", secret.Purpose, secret.CreatedBy))
			if len(secret.Fields) > 0 {
				m.AddRows(row.New(2))
				m.AddRows(wrappingTableHeader([]string{"Field", "Description", "Auto-generated"}, []int{3, 7, 2}))
				for _, f := range secret.Fields {
					autoGen := "No"
					if f.AutoGenerated {
						autoGen = "Yes"
					}
					m.AddRows(wrappingTableRow([]string{f.Name, f.Description, autoGen}, []int{3, 7, 2}))
				}
			}
			m.AddRows(row.New(4))
		}
	}

	// Control Room Connections
	if data.ControlRoom != nil && len(data.ControlRoom.Connections) > 0 {
		att.PdfSection(m, "Control Room Connections")
		att.PdfParagraph(m, fmt.Sprintf("This workload is connected to a control room in account %s (domain: %s).",
			data.ControlRoom.AccountID, data.ControlRoom.Domain))
		m.AddRows(row.New(3))

		m.AddRows(wrappingTableHeader([]string{"Category", "Resource", "Description"}, []int{3, 4, 5}))
		for _, conn := range data.ControlRoom.Connections {
			m.AddRows(wrappingTableRow([]string{conn.Category, conn.Resource, conn.Description}, []int{3, 4, 5}))
		}
		m.AddRows(row.New(4))

		// Severance Plan
		att.PdfSection(m, "Severance Plan")
		if data.DryRun {
			att.PdfParagraph(m, severancePlanDryRunText())
		} else {
			att.PdfParagraph(m, severancePlanLiveText())
		}
		m.AddRows(row.New(3))

		m.AddRows(wrappingTableHeader([]string{"Category", "Action"}, []int{4, 8}))
		for _, conn := range data.ControlRoom.Connections {
			m.AddRows(wrappingTableRow([]string{conn.Category, conn.SeverAction}, []int{4, 8}))
		}
		m.AddRows(row.New(4))
	}

	// Resource Inventory — categorized
	if len(data.Resources) > 0 {
		att.PdfSection(m, "Resource Inventory")

		totalResources := len(data.Resources)
		att.PdfParagraph(m, fmt.Sprintf("Total managed resources: %d, broken down by category below.", totalResources))
		m.AddRows(row.New(2))
		att.PdfParagraph(m, "This inventory is a snapshot taken at the time of the eject operation. For an accurate current reflection of resources, interrogate the live Pulumi state using the commands described in Pulumi State Ownership.")
		m.AddRows(row.New(2))
		att.PdfParagraph(m, arnReconstructionNote(cloud, data.AccountID, data.Region))
		m.AddRows(row.New(4))

		categorized := ResourcesByCategory(data.Resources)
		for _, cat := range OrderedCategories {
			resources := categorized[cat.Category]
			if len(resources) == 0 {
				continue
			}
			att.PdfSubSection(m, cat.Title)
			m.AddRows(wrappingTableHeader([]string{"Type", "Physical ID", "Stack"}, []int{3, 7, 2}))
			for _, r := range resources {
				m.AddRows(wrappingTableRow([]string{shortType(r.Type), compactPhysicalID(r.PhysicalID), r.Purpose}, []int{3, 7, 2}))
			}
			m.AddRows(row.New(4))
		}
	}

	// Generate
	doc, err := m.Generate()
	if err != nil {
		return fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.Save(outputPath)
}

// shortType extracts the readable resource type from a Pulumi type string.
// e.g., "aws:ec2/vpc:Vpc" → "ec2/vpc:Vpc", "aws:s3/bucket:Bucket" → "s3/bucket:Bucket"
func shortType(pulumiType string) string {
	parts := strings.SplitN(pulumiType, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return pulumiType
}

// compactPhysicalID strips the redundant arn:partition:service:region:account: prefix
// from AWS ARNs, keeping only the resource-type/resource-id portion that a customer
// needs to locate the resource in their console.
// e.g., "arn:aws:ec2:us-east-2:123456:vpc/vpc-0abc123" → "vpc/vpc-0abc123"
func compactPhysicalID(id string) string {
	if strings.HasPrefix(id, "arn:") {
		parts := strings.SplitN(id, ":", 6)
		if len(parts) == 6 {
			return parts[5]
		}
	}
	return id
}

var codeBlockBg = &props.Color{Red: 243, Green: 243, Blue: 248}

// pdfMultilineParagraph renders text that may contain \n characters.
// Backtick-wrapped segments are extracted and rendered as indented code blocks
// on their own line, in monospace font with a scoped background.
func pdfMultilineParagraph(m core.Maroto, content string) {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			m.AddRows(row.New(2))
			continue
		}
		if !strings.Contains(trimmed, "`") {
			att.PdfParagraph(m, strings.ReplaceAll(trimmed, "**", ""))
			continue
		}
		segments := splitBacktickSegments(trimmed)
		for _, seg := range segments {
			if seg.text == "" {
				continue
			}
			if seg.isCode {
				m.AddRows(row.New().Add(
					col.New(1),
					col.New(11).
						Add(text.New(seg.text, props.Text{
							Size:              8,
							Align:             align.Left,
							Family:            "courier",
							BreakLineStrategy: breakline.DashStrategy,
						})).
						WithStyle(&props.Cell{
							BackgroundColor: codeBlockBg,
						}),
				))
				m.AddRows(row.New(2))
			} else {
				m.AddRows(text.NewRow(6, seg.text, props.Text{
					Size:  9,
					Align: align.Left,
				}))
			}
		}
	}
}

type textSegment struct {
	text   string
	isCode bool
}

// splitBacktickSegments splits a string into alternating prose/code segments.
// e.g. "Set the backend: `pulumi login s3://foo`" →
//
//	[{text:"Set the backend: ", isCode:false}, {text:"pulumi login s3://foo", isCode:true}]
func splitBacktickSegments(s string) []textSegment {
	var segments []textSegment
	for {
		idx := strings.Index(s, "`")
		if idx == -1 {
			if s != "" {
				segments = append(segments, textSegment{text: s, isCode: false})
			}
			break
		}
		if idx > 0 {
			segments = append(segments, textSegment{text: s[:idx], isCode: false})
		}
		s = s[idx+1:]
		end := strings.Index(s, "`")
		if end == -1 {
			if s != "" {
				segments = append(segments, textSegment{text: s, isCode: false})
			}
			break
		}
		segments = append(segments, textSegment{text: s[:end], isCode: true})
		s = s[end+1:]
	}
	return segments
}

// wrappingTableHeader renders a table header row that auto-sizes to fit content.
func wrappingTableHeader(headers []string, sizes []int) core.Row {
	cols := make([]core.Col, len(headers))
	for i, h := range headers {
		cols[i] = col.New(sizes[i]).Add(text.New(h, props.Text{
			Size:  8,
			Style: fontstyle.Bold,
			Align: align.Left,
		}))
	}
	return row.New().Add(cols...).WithStyle(&props.Cell{
		BackgroundColor: att.HeaderBg,
		BorderType:      border.Bottom,
		BorderColor:     att.BorderColor,
		BorderThickness: 0.3,
	})
}

// wrappingTableRow renders a table data row that auto-sizes to fit wrapped text.
// Uses DashStrategy so long strings without spaces (ARNs, resource IDs) can wrap.
func wrappingTableRow(values []string, sizes []int) core.Row {
	cols := make([]core.Col, len(values))
	for i, v := range values {
		cols[i] = col.New(sizes[i]).Add(text.New(v, props.Text{
			Size:              8,
			Align:             align.Left,
			BreakLineStrategy: breakline.DashStrategy,
		}))
	}
	return row.New().Add(cols...).WithStyle(&props.Cell{
		BorderType:      border.Bottom,
		BorderColor:     att.BorderColor,
		BorderThickness: 0.2,
	})
}
