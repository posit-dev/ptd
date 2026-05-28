package attestation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
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
	HeaderBg    = &props.Color{Red: 240, Green: 240, Blue: 245}
	AccentColor = &props.Color{Red: 60, Green: 90, Blue: 150}
	MutedColor  = &props.Color{Red: 120, Green: 120, Blue: 120}
	BorderColor = &props.Color{Red: 200, Green: 200, Blue: 200}
)

// RenderPDF generates a PDF attestation document and writes it to the given path.
func RenderPDF(outputPath string, data *AttestationData) error {
	sort.Slice(data.Stacks, func(i, j int) bool {
		return StackOrder(data.Stacks[i].ProjectName) < StackOrder(data.Stacks[j].ProjectName)
	})

	cfg := config.NewBuilder().
		WithPageNumber(props.PageNumber{
			Pattern: "Page {current} of {total}",
			Place:   props.RightBottom,
			Size:    8,
			Color:   MutedColor,
		}).
		WithLeftMargin(18).
		WithTopMargin(18).
		WithRightMargin(18).
		Build()

	m := maroto.New(cfg)

	// Title
	m.AddRows(
		text.NewRow(16, data.DisplayTitle(), props.Text{
			Size:  20,
			Style: fontstyle.Bold,
			Align: align.Left,
			Color: AccentColor,
		}),
	)
	m.AddRows(line.NewRow(2, props.Line{
		Color:     AccentColor,
		Thickness: 1.5,
	}))
	m.AddRows(row.New(4))

	cloud := "aws"
	if data.Infra != nil && data.Infra.Cloud != "" {
		cloud = data.Infra.Cloud
	}

	// Metadata
	PdfMetaRow(m, "Environment", data.TargetName)
	PdfMetaRow(m, AccountLabel(cloud), data.AccountID)
	PdfMetaRow(m, "Region", data.Region)
	for _, site := range data.Sites {
		PdfMetaRow(m, "Site Domain", site.Domain)
	}
	PdfMetaRow(m, "Date", data.GeneratedAt.Format("2006-01-02"))

	m.AddRows(row.New(6))

	// Purpose
	PdfSection(m, "Purpose")
	PdfParagraph(m, purposeTextFor(cloud))

	m.AddRows(row.New(4))

	// Sites and products
	for _, site := range data.Sites {
		PdfSection(m, fmt.Sprintf("Installed Products — %s", site.SiteName))

		m.AddRows(PdfTableHeader([]string{"Product", "Version", "Domain"}, []int{4, 3, 5}))
		for _, product := range site.Products {
			domain := "—"
			if product.DomainPrefix != "" {
				domain = product.DomainPrefix + "." + site.Domain
			}
			m.AddRows(PdfTableRow([]string{ProductDisplayName(product.Name), product.Version, domain}, []int{4, 3, 5}))
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
			PdfSubSection(m, "Authentication Configuration")
			m.AddRows(PdfTableHeader([]string{"Product", "Method", "Identity Provider"}, []int{3, 1, 8}))
			for _, p := range site.Products {
				if p.Auth != nil {
					m.AddRows(PdfTableRow([]string{ProductDisplayName(p.Name), p.Auth.Type, p.Auth.Issuer}, []int{3, 1, 8}))
				}
			}
			m.AddRows(row.New(4))
		}
	}

	// Product summary
	if data.ProductSummary != "" {
		PdfParagraph(m, data.ProductSummary)
		m.AddRows(row.New(4))
	}

	// Infrastructure summary
	PdfSection(m, "Infrastructure Summary")
	PdfParagraph(m, infraSummaryTextFor(cloud))

	m.AddRows(row.New(3))

	totalResources := 0
	for _, s := range data.Stacks {
		totalResources += s.ResourceCount
	}
	if cloud == "azure" {
		PdfMetaRow(m, "State backend", data.StateBackendURL)
		PdfMetaRow(m, "Encryption", fmt.Sprintf("Azure Key Vault posit-team-dedicated in subscription %s", data.AccountID))
	} else {
		PdfMetaRow(m, "State backend", data.StateBackendURL)
		PdfMetaRow(m, "Encryption", fmt.Sprintf("AWS KMS key alias/posit-team-dedicated in account %s", data.AccountID))
	}

	m.AddRows(row.New(4))
	PdfSubSection(m, "Stack Overview")

	// Stack table
	m.AddRows(PdfTableHeader([]string{"Stack", "Purpose", "Resources"}, []int{3, 7, 2}))
	for _, stack := range data.Stacks {
		m.AddRows(PdfTableRow([]string{
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
	PdfSubSection(m, "Stack Details")
	for _, stack := range data.Stacks {
		stepName := stack.StepNameFromProject()
		prose := GenerateStackProse(stepName, data.Infra)
		if prose == "" && stack.Purpose != "" {
			prose = stack.Purpose
		}
		if prose != "" {
			PdfStackDetail(m, stack.ProjectName, prose)
		}
	}

	m.AddRows(row.New(2))

	// Custom steps
	if len(data.CustomSteps) > 0 {
		PdfSubSection(m, "Custom Steps")
		for _, step := range data.CustomSteps {
			if step.Enabled {
				PdfBullet(m, fmt.Sprintf("%s (inserted after %s%s): %s", step.Name, step.InsertAfter, step.InsertBefore, step.Description))
			}
		}
		m.AddRows(row.New(4))
	}

	// Verification
	PdfSection(m, "Verification")
	PdfSubSection(m, "Pulumi State Files")
	PdfParagraph(m, verificationTextFor(cloud))

	m.AddRows(row.New(3))

	PdfSubSection(m, "Expected State Files")
	m.AddRows(PdfTableHeader([]string{"File Path", "Stack"}, []int{7, 5}))
	for _, stack := range data.Stacks {
		m.AddRows(PdfTableRow([]string{stack.StateKey, stack.ProjectName}, []int{7, 5}))
	}

	m.AddRows(row.New(4))
	PdfParagraph(m, encryptionTextFor(cloud))

	// Tools
	m.AddRows(row.New(4))
	PdfSection(m, "Tools")
	PdfBullet(m, "ptd CLI — github.com/posit-dev/ptd — Open-source infrastructure tool that reads configuration files and converges the target to the declared state.")
	PdfBullet(m, "Pulumi — Infrastructure-as-code engine used by PTD to manage cloud resources declaratively.")
	PdfBullet(m, "Team Operator — Kubernetes operator (github.com/posit-dev/team-operator) that manages the lifecycle of Posit products within the cluster.")

	// Confirmation + Sign-off on a new page to avoid orphaned headers
	m.AddPages(page.New().Add(
		text.NewRow(10, "Confirmation", props.Text{
			Size:  14,
			Style: fontstyle.Bold,
			Align: align.Left,
			Color: AccentColor,
		}),
		line.NewRow(2, props.Line{
			Color:     BorderColor,
			Thickness: 0.5,
		}),
		row.New(2),
		text.NewRow(12, confirmationText, props.Text{
			Size:  9,
			Align: align.Left,
		}),
		row.New(6),
		text.NewRow(8, fmt.Sprintf("Generated: %s", data.GeneratedAt.Format("2006-01-02 15:04 MST")), props.Text{
			Size:  9,
			Style: fontstyle.Italic,
			Align: align.Left,
			Color: MutedColor,
		}),
		row.New(4),
		text.NewRow(10, "Sign-Off", props.Text{
			Size:  14,
			Style: fontstyle.Bold,
			Align: align.Left,
			Color: AccentColor,
		}),
		line.NewRow(2, props.Line{
			Color:     BorderColor,
			Thickness: 0.5,
		}),
		row.New(2),
		PdfTableHeader([]string{"", "Name", "Date"}, []int{3, 5, 4}),
		PdfSignatureRow("Prepared By", data.GeneratedAt.Format("2006-01-02")),
	))

	// Generate
	doc, err := m.Generate()
	if err != nil {
		return fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.Save(outputPath)
}

// PdfSection renders a section header with an accent-colored underline.
func PdfSection(m core.Maroto, title string) {
	m.AddRows(row.New(3))
	m.AddRows(
		text.NewRow(10, title, props.Text{
			Size:  14,
			Style: fontstyle.Bold,
			Align: align.Left,
			Color: AccentColor,
		}),
	)
	m.AddRows(line.NewRow(2, props.Line{
		Color:     BorderColor,
		Thickness: 0.5,
	}))
	m.AddRows(row.New(2))
}

// PdfSubSection renders a sub-section header.
func PdfSubSection(m core.Maroto, title string) {
	m.AddRows(
		text.NewRow(8, title, props.Text{
			Size:  11,
			Style: fontstyle.Bold,
			Align: align.Left,
		}),
	)
	m.AddRows(row.New(1))
}

// PdfMetaRow renders a label: value metadata pair.
func PdfMetaRow(m core.Maroto, label string, value string) {
	m.AddRows(
		row.New(6).Add(
			col.New(3).Add(text.New(label+":", props.Text{
				Size:  9,
				Style: fontstyle.Bold,
				Align: align.Left,
				Color: MutedColor,
			})),
			col.New(9).Add(text.New(value, props.Text{
				Size:  9,
				Align: align.Left,
			})),
		),
	)
}

// PdfParagraph renders a paragraph of text.
func PdfParagraph(m core.Maroto, content string) {
	m.AddRows(
		text.NewRow(12, content, props.Text{
			Size:  9,
			Align: align.Left,
		}),
	)
}

// PdfBullet renders a single bullet point.
func PdfBullet(m core.Maroto, content string) {
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

// PdfStackDetail renders a stack name and its prose description.
func PdfStackDetail(m core.Maroto, name string, prose string) {
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
			PdfBullet(m, l[2:])
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

// PdfTableHeader renders a shaded table header row.
func PdfTableHeader(headers []string, sizes []int) core.Row {
	cols := make([]core.Col, len(headers))
	for i, h := range headers {
		cols[i] = col.New(sizes[i]).Add(text.New(h, props.Text{
			Size:  8,
			Style: fontstyle.Bold,
			Align: align.Left,
		}))
	}
	return row.New(6).Add(cols...).WithStyle(&props.Cell{
		BackgroundColor: HeaderBg,
		BorderType:      border.Bottom,
		BorderColor:     BorderColor,
		BorderThickness: 0.3,
	})
}

// PdfTableRow renders a table data row with a subtle bottom border.
func PdfTableRow(values []string, sizes []int) core.Row {
	cols := make([]core.Col, len(values))
	for i, v := range values {
		cols[i] = col.New(sizes[i]).Add(text.New(v, props.Text{
			Size:  8,
			Align: align.Left,
		}))
	}
	return row.New(6).Add(cols...).WithStyle(&props.Cell{
		BorderType:      border.Bottom,
		BorderColor:     BorderColor,
		BorderThickness: 0.2,
	})
}

// PdfSignatureRow renders a signature line row with the role label, a blank
// name field, and an optional pre-filled date.
func PdfSignatureRow(role, date string) core.Row {
	return row.New(12).Add(
		col.New(3).Add(text.New(role, props.Text{
			Size:  9,
			Style: fontstyle.Bold,
			Align: align.Left,
		})),
		col.New(5).Add(text.New("", props.Text{Size: 9})),
		col.New(4).Add(text.New(date, props.Text{Size: 9})),
	).WithStyle(&props.Cell{
		BorderType:      border.Bottom,
		BorderColor:     BorderColor,
		BorderThickness: 0.3,
	})
}
