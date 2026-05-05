package api

import (
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/xuri/excelize/v2"
)

// GET /stats/export?days=7&format=csv|xml|xls|xlsx&report=daily|sessions|types|hours|contacts
func (h *handlers) exportStats(c *fiber.Ctx) error {
	days, _ := strconv.Atoi(c.Query("days", "7"))
	if days < 1 || days > 90 {
		days = 7
	}
	format := c.Query("format", "csv")
	report := c.Query("report", "daily")

	// Monta os dados em função do relatório
	headers, rows, title, err := h.buildReportData(c, report, days)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	filename := fmt.Sprintf("relatorio_%s_%ddays_%s", report, days, time.Now().Format("20060102"))

	switch format {
	case "csv":
		return h.exportCSV(c, headers, rows, filename)
	case "xml":
		return h.exportXML(c, headers, rows, title, filename)
	case "xlsx", "xls":
		return h.exportXLSX(c, headers, rows, title, filename, format)
	default:
		return fiber.NewError(fiber.StatusBadRequest, "formato inválido: use csv, xml, xlsx ou xls")
	}
}

// buildReportData busca os dados e retorna headers + rows genéricos
func (h *handlers) buildReportData(c *fiber.Ctx, report string, days int) (headers []string, rows [][]string, title string, err error) {
	switch report {
	case "daily":
		title = "Volume Diário"
		headers = []string{"Data", "Total", "Recebidas", "Enviadas"}
		data, e := h.repo.GetDailyStats(c.Context(), days)
		if e != nil {
			return nil, nil, "", e
		}
		for _, r := range data {
			rows = append(rows, []string{
				r.Date.Format("02/01/2006"),
				strconv.FormatInt(r.TotalMessages, 10),
				strconv.FormatInt(r.InboundCount, 10),
				strconv.FormatInt(r.OutboundCount, 10),
			})
		}

	case "sessions":
		title = "Por Número WhatsApp"
		headers = []string{"Número", "JID", "Total", "Recebidas", "Enviadas", "Falhas"}
		data, e := h.repo.GetStatsBySession(c.Context(), days)
		if e != nil {
			return nil, nil, "", e
		}
		for _, r := range data {
			tel := "+" + r.Phone
			if r.Phone == "" || r.Phone == "desconhecido" {
				tel = r.SessionJID
			}
			rows = append(rows, []string{
				tel,
				r.SessionJID,
				strconv.FormatInt(r.TotalMessages, 10),
				strconv.FormatInt(r.InboundCount, 10),
				strconv.FormatInt(r.OutboundCount, 10),
				strconv.FormatInt(r.FailedCount, 10),
			})
		}

	case "types":
		title = "Por Tipo de Mensagem"
		headers = []string{"Tipo", "Total", "Recebidas", "Enviadas"}
		data, e := h.repo.GetStatsByType(c.Context(), days)
		if e != nil {
			return nil, nil, "", e
		}
		for _, r := range data {
			rows = append(rows, []string{
				r.MessageType,
				strconv.FormatInt(r.TotalMessages, 10),
				strconv.FormatInt(r.InboundCount, 10),
				strconv.FormatInt(r.OutboundCount, 10),
			})
		}

	case "hours":
		title = "Volume por Hora do Dia"
		headers = []string{"Hora", "Total"}
		data, e := h.repo.GetStatsByHour(c.Context(), days)
		if e != nil {
			return nil, nil, "", e
		}
		for _, r := range data {
			rows = append(rows, []string{
				fmt.Sprintf("%02dh", r.Hour),
				strconv.FormatInt(r.TotalMessages, 10),
			})
		}

	case "contacts":
		title = "Top Contatos"
		headers = []string{"Nome", "Telefone", "JID", "Total", "Recebidas", "Enviadas"}
		data, e := h.repo.GetTopContacts(c.Context(), days, 100)
		if e != nil {
			return nil, nil, "", e
		}
		for _, r := range data {
			tel := ""
			if r.WAPhone != "" {
				tel = "+" + r.WAPhone
			}
			rows = append(rows, []string{
				r.WAName,
				tel,
				r.WAJID,
				strconv.FormatInt(r.TotalMessages, 10),
				strconv.FormatInt(r.InboundCount, 10),
				strconv.FormatInt(r.OutboundCount, 10),
			})
		}

	default:
		return nil, nil, "", fmt.Errorf("relatório inválido: use daily, sessions, types, hours ou contacts")
	}

	return headers, rows, title, nil
}

func (h *handlers) exportCSV(c *fiber.Ctx, headers []string, rows [][]string, filename string) error {
	var buf bytes.Buffer
	// BOM UTF-8 para o Excel abrir corretamente
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(&buf)
	w.Comma = ';'
	_ = w.Write(headers)
	for _, row := range rows {
		_ = w.Write(row)
	}
	w.Flush()

	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, filename))
	return c.Send(buf.Bytes())
}

func (h *handlers) exportXML(c *fiber.Ctx, headers []string, rows [][]string, title, filename string) error {
	type Field struct {
		XMLName xml.Name
		Value   string `xml:",chardata"`
	}
	type Row struct {
		Fields []Field
	}
	type Report struct {
		XMLName   xml.Name `xml:"relatorio"`
		Title     string   `xml:"titulo"`
		GeneratedAt string `xml:"gerado_em"`
		Rows      []interface{}
	}

	// Monta XML dinamicamente
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	buf.WriteString(fmt.Sprintf("<relatorio>\n  <titulo>%s</titulo>\n  <gerado_em>%s</gerado_em>\n  <dados>\n",
		xmlEscape(title), time.Now().Format("02/01/2006 15:04:05")))

	for _, row := range rows {
		buf.WriteString("    <registro>\n")
		for i, val := range row {
			colName := "coluna"
			if i < len(headers) {
				colName = xmlTagName(headers[i])
			}
			buf.WriteString(fmt.Sprintf("      <%s>%s</%s>\n", colName, xmlEscape(val), colName))
		}
		buf.WriteString("    </registro>\n")
	}
	buf.WriteString("  </dados>\n</relatorio>")

	c.Set("Content-Type", "application/xml; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xml"`, filename))
	return c.Send(buf.Bytes())
}

func (h *handlers) exportXLSX(c *fiber.Ctx, headers []string, rows [][]string, title, filename, format string) error {
	f := excelize.NewFile()
	defer f.Close()

	sheet := "Relatório"
	f.SetSheetName("Sheet1", sheet)

	// Estilos
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"1A3A2A"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "bottom", Color: "25D366", Style: 2},
		},
	})
	altStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Color: []string{"F0FFF4"}, Pattern: 1},
	})
	numStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "right"},
	})

	// Título da planilha
	f.MergeCell(sheet, "A1", colName(len(headers))+"1")
	titleStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 13, Color: "1A3A2A"},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	f.SetCellStyle(sheet, "A1", colName(len(headers))+"1", titleStyle)
	f.SetCellValue(sheet, "A1", title)
	f.SetRowHeight(sheet, 1, 28)

	// Sub-título com período
	f.MergeCell(sheet, "A2", colName(len(headers))+"2")
	subStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Size: 10, Color: "64748B", Italic: true},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	f.SetCellStyle(sheet, "A2", colName(len(headers))+"2", subStyle)
	f.SetCellValue(sheet, "A2", "Gerado em "+time.Now().Format("02/01/2006 15:04"))
	f.SetRowHeight(sheet, 2, 18)

	// Headers na linha 3
	for i, h := range headers {
		cell := colName(i+1) + "3"
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}
	f.SetRowHeight(sheet, 3, 22)

	// Dados a partir da linha 4
	for ri, row := range rows {
		rowNum := ri + 4
		for ci, val := range row {
			cell := colName(ci+1) + strconv.Itoa(rowNum)
			// Tenta converter para número
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				f.SetCellValue(sheet, cell, n)
				f.SetCellStyle(sheet, cell, cell, numStyle)
			} else {
				f.SetCellValue(sheet, cell, val)
			}
			// Linha alternada
			if ri%2 == 1 {
				f.SetCellStyle(sheet, cell, cell, altStyle)
			}
		}
		f.SetRowHeight(sheet, rowNum, 18)
	}

	// Auto-largura das colunas
	for i := range headers {
		col := colName(i + 1)
		f.SetColWidth(sheet, col, col, 18)
	}

	// Congela a linha de cabeçalho
	f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      3,
		TopLeftCell: "A4",
		ActivePane:  "bottomLeft",
	})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	ext := "xlsx"
	mime := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	if format == "xls" {
		ext = "xls"
		mime = "application/vnd.ms-excel"
	}

	c.Set("Content-Type", mime)
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, filename, ext))
	return c.Send(buf.Bytes())
}

// colName converte índice (1-based) para letra de coluna Excel: 1→A, 26→Z, 27→AA
func colName(n int) string {
	name := ""
	for n > 0 {
		n--
		name = string(rune('A'+n%26)) + name
		n /= 26
	}
	return name
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func xmlTagName(s string) string {
	var out []byte
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, byte(c))
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "campo"
	}
	// XML tag não pode começar com número
	if out[0] >= '0' && out[0] <= '9' {
		out = append([]byte{'c'}, out...)
	}
	return string(out)
}
