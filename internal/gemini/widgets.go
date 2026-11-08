package gemini

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// widgetTags 是官网在正文中渲染为卡片的组件：步骤卡片、模板卡片与视频轮播
var widgetTags = []string{"Sequence", "TemplateWidget", "Carousel"}

// widgetOpenPattern 匹配行首的组件开标签
var widgetOpenPattern = regexp.MustCompile(`^<(Sequence|TemplateWidget|Carousel)(?:[\s>]|$)`)

// widgetAttributePattern 匹配组件标签中的一个属性
var widgetAttributePattern = regexp.MustCompile(`([A-Za-z_][\w-]*)="([^"]*)"`)

// videoPlaceholderPattern 匹配视频轮播中占位编号对应富内容第 5 项分组的序号
var videoPlaceholderPattern = regexp.MustCompile(`^youtube_video_(\d+)$`)

// renderWidgets 把代码块以外行首的步骤卡片、清单模板与视频轮播转为 Markdown；未闭合的 Sequence 放出已闭合的 Step，其余未闭合组件与可能是组件开头的末行从该行起截去
func renderWidgets(candidate []any, raw string) string {
	if !strings.Contains(raw, "<") {
		return raw
	}
	lines := strings.Split(raw, "\n")
	var output []string
	fence := ""
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		switch {
		case fence != "":
			if closesFence(line, fence) {
				fence = ""
			}
			output = append(output, line)
			continue
		case fenceOpening(line) != "":
			fence = fenceOpening(line)
			output = append(output, line)
			continue
		}
		if index == len(lines)-1 && widgetPrefix(trimmed) {
			break
		}
		match := widgetOpenPattern.FindStringSubmatch(trimmed)
		if match == nil {
			output = append(output, line)
			continue
		}
		end := -1
		for next := index + 1; next < len(lines); next++ {
			if strings.TrimSpace(lines[next]) == "</"+match[1]+">" {
				end = next
				break
			}
		}
		if end < 0 {
			if match[1] == "Sequence" {
				if rendered := renderSequence(lines[index+1:]); rendered != "" {
					output = append(output, rendered)
				}
			}
			break
		}
		if match[1] == "Carousel" && !slices.ContainsFunc(lines[index+1:end], func(child string) bool { return strings.HasPrefix(strings.TrimSpace(child), "<Video") }) {
			output = append(output, lines[index:end+1]...)
			index = end
			continue
		}
		if rendered := renderWidget(candidate, match[1], widgetAttributes(trimmed), lines[index+1:end]); rendered != "" {
			output = append(output, rendered)
		}
		index = end
	}
	return strings.Join(output, "\n")
}

// widgetPrefix 判断末行是否可能是尚未写完的组件开标签
func widgetPrefix(line string) bool {
	if !strings.HasPrefix(line, "<") || strings.HasSuffix(line, ">") {
		return false
	}
	name, _, _ := strings.Cut(line[1:], " ")
	for _, tag := range widgetTags {
		if strings.HasPrefix(tag, name) || name == tag {
			return true
		}
	}
	return false
}

// widgetAttributes 返回组件开标签的属性
func widgetAttributes(tag string) map[string]string {
	attributes := make(map[string]string)
	for _, match := range widgetAttributePattern.FindAllStringSubmatch(tag, -1) {
		attributes[match[1]] = html.UnescapeString(match[2])
	}
	return attributes
}

// renderWidget 把一个组件的内容行转为 Markdown，数据对不上的视频轮播返回空
func renderWidget(candidate []any, name string, attributes map[string]string, body []string) string {
	switch name {
	case "Sequence":
		return renderSequence(body)
	case "Carousel":
		return renderVideoCarousel(candidate, body)
	}
	return renderTemplate(attributes, body)
}

// renderSequence 把每个已闭合的 Step 写为带序号的粗体标题、副标题与去掉公共缩进的正文，尚未闭合的 Step 不输出
func renderSequence(body []string) string {
	var steps []string
	for index := 0; index < len(body); index++ {
		tag := strings.TrimSpace(body[index])
		if !strings.HasPrefix(tag, "<Step") {
			continue
		}
		attributes := widgetAttributes(tag)
		var content []string
		if !strings.HasSuffix(tag, "/>") {
			closing := slices.IndexFunc(body[index+1:], func(line string) bool { return strings.TrimSpace(line) == "</Step>" })
			if closing < 0 {
				break
			}
			content = body[index+1 : index+1+closing]
			index += 1 + closing
		}
		step := fmt.Sprintf("**%d. %s**", len(steps)+1, attributes["title"])
		if attributes["subtitle"] != "" {
			step += " — " + attributes["subtitle"]
		}
		if text := strings.TrimSpace(dedent(content)); text != "" {
			step += "\n\n" + text
		}
		steps = append(steps, step)
	}
	return strings.Join(steps, "\n\n")
}

// dedent 去掉各行共同的前导空白，只含空白的行写为空行
func dedent(lines []string) string {
	common := -1
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if common < 0 || indent < common {
				common = indent
			}
		}
	}
	result := make([]string, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) != "" {
			result[index] = line[common:]
		}
	}
	return strings.Join(result, "\n")
}

// renderVideoCarousel 把视频占位换成富内容第 5 项分组中的视频链接列表
func renderVideoCarousel(candidate []any, body []string) string {
	groups, _ := richContentField(candidate, 4)
	list, _ := groups.([]any)
	var items []string
	for _, line := range body {
		match := videoPlaceholderPattern.FindStringSubmatch(widgetAttributes(line)["component_placeholder_id"])
		if match == nil {
			continue
		}
		group, _ := strconv.Atoi(match[1])
		if group >= len(list) {
			continue
		}
		videos, _ := valuePath(list[group], 4, 0)
		entries, _ := videos.([]any)
		for _, entry := range entries {
			title, _ := stringPath(entry, 0)
			link, _ := stringPath(entry, 2)
			channel, _ := stringPath(entry, 3)
			if title == "" || link == "" {
				continue
			}
			item := fmt.Sprintf("- [%s](%s)", title, link)
			if channel != "" {
				item += " — " + channel
			}
			items = append(items, item)
		}
	}
	return strings.Join(items, "\n")
}

// renderTemplate 把模板卡片写为粗体标题加内容：清单模板的 JSON 按分类写为任务列表，其他模板保留原内容
func renderTemplate(attributes map[string]string, body []string) string {
	head := ""
	if attributes["header_title"] != "" {
		head = "**" + attributes["header_title"] + "**\n\n"
	}
	content := strings.TrimSpace(dedent(body))
	if attributes["templateName"] == "checklist" {
		if list := renderChecklist(content); list != "" {
			return head + list
		}
	}
	return strings.TrimSpace(head + content)
}

// renderChecklist 把清单模板代码块中的 JSON 按分类顺序写为任务列表，无法解析时返回空
func renderChecklist(content string) string {
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	var data struct {
		Categories []string `json:"categories"`
		Items      []struct {
			Label    string   `json:"label"`
			Category string   `json:"category"`
			Sublines []string `json:"sublines"`
			Checked  bool     `json:"checked"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(content), &data) != nil || len(data.Items) == 0 {
		return ""
	}
	categories := slices.Clone(data.Categories)
	for _, item := range data.Items {
		if !slices.Contains(categories, item.Category) {
			categories = append(categories, item.Category)
		}
	}
	var sections []string
	for _, category := range categories {
		var lines []string
		if category != "" {
			lines = append(lines, "**"+category+"**")
		}
		for _, item := range data.Items {
			if item.Category != category {
				continue
			}
			box := "[ ]"
			if item.Checked {
				box = "[x]"
			}
			line := "- " + box + " " + item.Label
			if len(item.Sublines) > 0 {
				line += " — " + strings.Join(item.Sublines, "; ")
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "- ") {
			sections = append(sections, strings.Join(lines, "\n"))
		}
	}
	return strings.Join(sections, "\n\n")
}
