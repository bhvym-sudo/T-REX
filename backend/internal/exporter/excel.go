package exporter

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	"trex/backend/internal/model"
)

func PostsToExcel(posts []model.Post, output string) error {
	if len(posts) == 0 {
		return fmt.Errorf("there are no posts to export")
	}
	if strings.ToLower(filepath.Ext(output)) != ".xlsx" {
		output += ".xlsx"
	}
	file := excelize.NewFile()
	sheet := "Posts"
	file.SetSheetName("Sheet1", sheet)
	headers := []string{
		"Sr No", "Tweet ID", "Created At", "Tweet URL", "Message", "Query",
		"Author Name", "Author Screen Name", "Author ID", "Author Verified", "Author Blue Verified",
		"Author Followers", "Author Following", "Author Location", "Author Bio",
		"Replies", "Reposts", "Quotes", "Likes", "Bookmarks", "Views", "Entities", "Media",
	}
	writeHeader(file, sheet, headers)
	for index, post := range posts {
		row := []any{
			index + 1, post.ID, post.CreatedAt, post.URL, post.Text, post.Query,
			post.Author["name"], post.Author["screen_name"], post.Author["id"], post.Author["verified"], post.Author["is_blue_verified"],
			post.Author["followers_count"], post.Author["following_count"], post.Author["location"], post.Author["description"],
			post.Metrics["reply_count"], post.Metrics["retweet_count"], post.Metrics["quote_count"], post.Metrics["like_count"],
			post.Metrics["bookmark_count"], post.Metrics["view_count"], jsonText(post.Entities), jsonText(post.Media),
		}
		for column, value := range row {
			cell, _ := excelize.CoordinatesToCellName(column+1, index+2)
			_ = file.SetCellValue(sheet, cell, value)
		}
	}
	styleWorkbook(file, sheet, len(headers), len(posts)+1)
	return file.SaveAs(output)
}

func AccountToExcel(account model.AccountRecord, output string) error {
	if account.ScreenName == "" {
		return fmt.Errorf("account data is empty")
	}
	if strings.ToLower(filepath.Ext(output)) != ".xlsx" {
		output += ".xlsx"
	}
	file := excelize.NewFile()
	file.SetSheetName("Sheet1", "Account")
	writeHeader(file, "Account", []string{"Section", "Field", "Value"})
	row := 2
	for _, section := range account.Sections {
		for _, field := range section.Rows {
			_ = file.SetCellValue("Account", fmt.Sprintf("A%d", row), section.Title)
			_ = file.SetCellValue("Account", fmt.Sprintf("B%d", row), field.Label)
			_ = file.SetCellValue("Account", fmt.Sprintf("C%d", row), valueText(field.Value))
			row++
		}
	}
	addRawSheet(file, "UserByScreenName", account.RawProfile)
	addRawSheet(file, "AboutAccount", account.RawAbout)
	styleWorkbook(file, "Account", 3, row-1)
	return file.SaveAs(output)
}

func AuthorsToExcel(posts []model.Post, accounts map[string]model.AccountRecord, output string) error {
	if len(posts) == 0 {
		return fmt.Errorf("there are no posts to export")
	}
	if strings.ToLower(filepath.Ext(output)) != ".xlsx" {
		output += ".xlsx"
	}
	keys := map[string]bool{}
	for _, account := range accounts {
		for key, value := range account.Fields {
			if value != nil {
				keys[key] = true
			}
		}
	}
	authorFields := make([]string, 0, len(keys))
	for key := range keys {
		authorFields = append(authorFields, key)
	}
	sort.Strings(authorFields)
	headers := []string{"Sr No", "Message", "Tweet URL"}
	for _, key := range authorFields {
		headers = append(headers, "Author / "+humanize(key))
	}
	file := excelize.NewFile()
	file.SetSheetName("Sheet1", "Authors")
	writeHeader(file, "Authors", headers)
	for index, post := range posts {
		handle := strings.ToLower(fmt.Sprint(post.Author["screen_name"]))
		account := accounts[handle]
		row := []any{index + 1, post.Text, post.URL}
		for _, key := range authorFields {
			row = append(row, valueText(account.Fields[key]))
		}
		for column, value := range row {
			cell, _ := excelize.CoordinatesToCellName(column+1, index+2)
			_ = file.SetCellValue("Authors", cell, value)
		}
	}
	styleWorkbook(file, "Authors", len(headers), len(posts)+1)
	return file.SaveAs(output)
}

func RepliesToExcel(tweet model.Post, replies []model.Post, output string) error {
	if strings.ToLower(filepath.Ext(output)) != ".xlsx" {
		output += ".xlsx"
	}
	file := excelize.NewFile()
	file.SetSheetName("Sheet1", "Tweet")
	writeHeader(file, "Tweet", []string{"Field", "Value"})
	tweetRows := [][]any{
		{"Tweet ID", tweet.ID}, {"Created At", tweet.CreatedAt}, {"URL", tweet.URL},
		{"Author", tweet.Author["screen_name"]}, {"Text", tweet.Text},
		{"Replies Collected", len(replies)}, {"Exported At", time.Now().Format(time.RFC3339)},
	}
	for index, row := range tweetRows {
		_ = file.SetCellValue("Tweet", fmt.Sprintf("A%d", index+2), row[0])
		_ = file.SetCellValue("Tweet", fmt.Sprintf("B%d", index+2), row[1])
	}
	_, _ = file.NewSheet("Replies")
	headers := []string{"Sr No", "Reply ID", "Created At", "Reply URL", "Reply Text", "Author", "Author Name", "Likes", "Replies", "Reposts"}
	writeHeader(file, "Replies", headers)
	for index, reply := range replies {
		row := []any{index + 1, reply.ID, reply.CreatedAt, reply.URL, reply.Text, reply.Author["screen_name"], reply.Author["name"], reply.Metrics["like_count"], reply.Metrics["reply_count"], reply.Metrics["retweet_count"]}
		for column, value := range row {
			cell, _ := excelize.CoordinatesToCellName(column+1, index+2)
			_ = file.SetCellValue("Replies", cell, value)
		}
	}
	styleWorkbook(file, "Tweet", 2, len(tweetRows)+1)
	styleWorkbook(file, "Replies", len(headers), len(replies)+1)
	return file.SaveAs(output)
}

func writeHeader(file *excelize.File, sheet string, headers []string) {
	for index, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(index+1, 1)
		_ = file.SetCellValue(sheet, cell, header)
	}
}

func styleWorkbook(file *excelize.File, sheet string, columns, rows int) {
	headerStyle, _ := file.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"20242A"}, Pattern: 1},
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	bodyStyle, _ := file.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
	})
	end, _ := excelize.CoordinatesToCellName(columns, 1)
	_ = file.SetCellStyle(sheet, "A1", end, headerStyle)
	if rows > 1 {
		bodyEnd, _ := excelize.CoordinatesToCellName(columns, rows)
		_ = file.SetCellStyle(sheet, "A2", bodyEnd, bodyStyle)
	}
	_ = file.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = file.AutoFilter(sheet, "A1:"+end, nil)
	for column := 1; column <= columns; column++ {
		name, _ := excelize.ColumnNumberToName(column)
		width := 18.0
		if column == 5 || column == 3 {
			width = 56
		}
		_ = file.SetColWidth(sheet, name, name, width)
	}
}

func addRawSheet(file *excelize.File, name string, value map[string]any) {
	_, _ = file.NewSheet(name)
	writeHeader(file, name, []string{"Field", "Value"})
	flat := map[string]any{}
	flatten("", value, flat)
	keys := make([]string, 0, len(flat))
	for key := range flat {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index, key := range keys {
		_ = file.SetCellValue(name, fmt.Sprintf("A%d", index+2), key)
		_ = file.SetCellValue(name, fmt.Sprintf("B%d", index+2), valueText(flat[key]))
	}
	styleWorkbook(file, name, 2, len(keys)+1)
}

func flatten(prefix string, value any, output map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flatten(next, child, output)
		}
	case []any:
		for index, child := range typed {
			flatten(fmt.Sprintf("%s.%d", prefix, index+1), child, output)
		}
	default:
		output[prefix] = typed
	}
}

func valueText(value any) string {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "Yes"
		}
		return "No"
	case string:
		return typed
	default:
		return jsonText(value)
	}
}

func humanize(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	parts := strings.Fields(value)
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func jsonText(value any) string {
	if value == nil {
		return ""
	}
	data, _ := json.Marshal(value)
	return string(data)
}
