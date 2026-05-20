package service

import (
	"regexp"
	"strconv"
	"strings"
)

// htmlInAnnouncement reports whether the announcement content still contains the
// legacy HTML tag set we used to allow (the editor produced <p>, <h3>, <strong>,
// <a>, <ul>, <ol>, <blockquote>, <br>). Plain Markdown content has none of these.
func htmlInAnnouncement(content string) bool {
	if content == "" {
		return false
	}
	return announcementHTMLTagPattern.MatchString(content)
}

var announcementHTMLTagPattern = regexp.MustCompile(`(?i)<\s*/?\s*(p|br|h[1-6]|strong|b|em|i|u|a|ul|ol|li|blockquote|hr|div|span|small|code|pre)([\s>/])`)

// announcementHTMLToMarkdown converts the historical announcement HTML payloads
// into the Markdown subset that AnnouncementMarkdown understands. The conversion
// is intentionally narrow — it covers only the tags the textarea editor offered
// (h3, strong/b, em/i, a, ul/ol/li, blockquote, br, p, hr) and falls back to the
// stripped text for anything else. Anything that's already plain Markdown stays
// untouched because htmlInAnnouncement returns false for it.
func announcementHTMLToMarkdown(content string) string {
	if !htmlInAnnouncement(content) {
		return content
	}

	out := content

	// Normalise newlines first so the line-based passes below behave.
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "\n")

	// Block-level: drop wrapping divs, keep their content.
	out = stripTag(out, "div")
	out = stripTag(out, "span")
	out = stripTag(out, "small")

	// Headings: <h1> .. <h6> -> "# ".."###### "
	for level := 6; level >= 1; level-- {
		open := regexp.MustCompile(`(?is)<\s*h` + strconv.Itoa(level) + `\b[^>]*>`)
		close := regexp.MustCompile(`(?is)<\s*/\s*h` + strconv.Itoa(level) + `\b\s*>`)
		marker := strings.Repeat("#", level) + " "
		out = open.ReplaceAllString(out, "\n\n"+marker)
		out = close.ReplaceAllString(out, "\n\n")
	}

	// Inline emphasis.
	out = wrapTag(out, "strong", "**")
	out = wrapTag(out, "b", "**")
	out = wrapTag(out, "em", "*")
	out = wrapTag(out, "i", "*")
	out = wrapTag(out, "code", "`")

	// Underline has no Markdown equivalent — keep raw text.
	out = stripTag(out, "u")

	// Links: <a href="X" ...>Y</a> -> [Y](X). Preserve raw URL when href absent.
	out = announcementAnchorPattern.ReplaceAllStringFunc(out, func(match string) string {
		groups := announcementAnchorPattern.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		href := strings.TrimSpace(groups[1])
		text := strings.TrimSpace(stripAllTags(groups[2]))
		if text == "" {
			text = href
		}
		if href == "" {
			return text
		}
		return "[" + text + "](" + href + ")"
	})

	// Lists.
	out = convertList(out, "ul", false)
	out = convertList(out, "ol", true)

	// Blockquote.
	out = announcementBlockquotePattern.ReplaceAllStringFunc(out, func(match string) string {
		body := announcementBlockquotePattern.FindStringSubmatch(match)
		if len(body) < 2 {
			return match
		}
		inner := strings.TrimSpace(stripAllTags(body[1]))
		if inner == "" {
			return ""
		}
		var lines []string
		for _, line := range strings.Split(inner, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines = append(lines, "> "+line)
		}
		return "\n" + strings.Join(lines, "\n") + "\n"
	})

	// <br> -> newline.
	out = announcementBrPattern.ReplaceAllString(out, "\n")

	// Paragraphs: opening tag becomes blank line separator, closing tag too.
	out = announcementPOpenPattern.ReplaceAllString(out, "\n\n")
	out = announcementPClosePattern.ReplaceAllString(out, "\n\n")

	// Horizontal rule.
	out = announcementHrPattern.ReplaceAllString(out, "\n\n---\n\n")

	// Anything else still tagged: strip it but keep textual content.
	out = stripAllTags(out)

	// HTML entities we commonly see.
	out = decodeEntities(out)

	// Collapse runs of blank lines.
	out = announcementBlankLinesPattern.ReplaceAllString(out, "\n\n")

	return strings.TrimSpace(out)
}

var (
	announcementAnchorPattern     = regexp.MustCompile(`(?is)<\s*a\b[^>]*?\bhref\s*=\s*"([^"]*)"[^>]*>(.*?)<\s*/\s*a\s*>`)
	announcementBlockquotePattern = regexp.MustCompile(`(?is)<\s*blockquote[^>]*>(.*?)<\s*/\s*blockquote\s*>`)
	announcementBrPattern         = regexp.MustCompile(`(?i)<\s*br\s*/?\s*>`)
	announcementPOpenPattern      = regexp.MustCompile(`(?i)<\s*p[^>]*>`)
	announcementPClosePattern     = regexp.MustCompile(`(?i)<\s*/\s*p\s*>`)
	announcementHrPattern         = regexp.MustCompile(`(?i)<\s*hr\s*/?\s*>`)
	announcementBlankLinesPattern = regexp.MustCompile(`\n{3,}`)
	announcementAnyTagPattern     = regexp.MustCompile(`(?is)<[^>]+>`)
)

func wrapTag(input, tag, marker string) string {
	open := regexp.MustCompile(`(?is)<\s*` + tag + `\b[^>]*>`)
	close := regexp.MustCompile(`(?is)<\s*/\s*` + tag + `\b\s*>`)
	input = open.ReplaceAllString(input, marker)
	input = close.ReplaceAllString(input, marker)
	return input
}

func stripTag(input, tag string) string {
	open := regexp.MustCompile(`(?is)<\s*` + tag + `\b[^>]*>`)
	close := regexp.MustCompile(`(?is)<\s*/\s*` + tag + `\b\s*>`)
	input = open.ReplaceAllString(input, "")
	input = close.ReplaceAllString(input, "")
	return input
}

func stripAllTags(input string) string {
	return announcementAnyTagPattern.ReplaceAllString(input, "")
}

func convertList(input, tag string, ordered bool) string {
	pattern := regexp.MustCompile(`(?is)<\s*` + tag + `\b[^>]*>(.*?)<\s*/\s*` + tag + `\b\s*>`)
	return pattern.ReplaceAllStringFunc(input, func(match string) string {
		body := pattern.FindStringSubmatch(match)
		if len(body) < 2 {
			return match
		}
		listBody := body[1]
		itemPattern := regexp.MustCompile(`(?is)<\s*li\b[^>]*>(.*?)<\s*/\s*li\b\s*>`)
		matches := itemPattern.FindAllStringSubmatch(listBody, -1)
		if len(matches) == 0 {
			return ""
		}
		var lines []string
		for index, item := range matches {
			text := strings.TrimSpace(stripAllTags(item[1]))
			if text == "" {
				continue
			}
			marker := "- "
			if ordered {
				marker = strconv.Itoa(index+1) + ". "
			}
			lines = append(lines, marker+text)
		}
		if len(lines) == 0 {
			return ""
		}
		return "\n" + strings.Join(lines, "\n") + "\n"
	})
}

func decodeEntities(input string) string {
	replacer := strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
	)
	return replacer.Replace(input)
}
