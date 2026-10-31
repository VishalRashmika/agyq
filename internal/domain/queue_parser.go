package domain

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var headingRegex = regexp.MustCompile(`^#[ \t]+#(\d+)(?:[:\-\x{2013}\x{2014}]?[ \t]*(.*?))?[ \t]*$`)

// ParseQueueText parses a Markdown queue from a string. It is a pure function
// that does not touch OS, clock, or filesystem.
func ParseQueueText(content string) ([]Prompt, error) {
	// Strip UTF-8 BOM if present
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")

	// Normalize CRLF to LF
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	lines := strings.Split(content, "\n")

	var prompts []Prompt
	var currentPrompt *Prompt
	var currentBodyLines []string

	inFence := false
	fenceChar := byte(0)
	fenceLen := 0

	finalizePrompt := func() {
		if currentPrompt == nil {
			return
		}
		// Trim leading and trailing blank lines from body
		start := 0
		for start < len(currentBodyLines) && isBlankLine(currentBodyLines[start]) {
			start++
		}
		end := len(currentBodyLines)
		for end > start && isBlankLine(currentBodyLines[end-1]) {
			end--
		}
		body := ""
		if start < end {
			body = strings.Join(currentBodyLines[start:end], "\n")
		}

		// Check for <!-- agyq:skip --> directly under heading
		if start < len(currentBodyLines) {
			trimmedFirst := strings.TrimSpace(currentBodyLines[start])
			if trimmedFirst == "<!-- agyq:skip -->" {
				currentPrompt.Skip = true
			}
		}

		currentPrompt.Body = body
		currentPrompt.Hash = ComputePromptHash(currentPrompt.Title, body)
		prompts = append(prompts, *currentPrompt)
		currentPrompt = nil
		currentBodyLines = nil
	}

	for i, line := range lines {
		lineNum := i + 1

		// Check code fence
		isFence, fChar, fLen, isCloser := parseFence(line, inFence, fenceChar, fenceLen)
		if isFence {
			if !inFence {
				inFence = true
				fenceChar = fChar
				fenceLen = fLen
			} else if isCloser {
				inFence = false
				fenceChar = 0
				fenceLen = 0
			}
			if currentPrompt != nil {
				currentBodyLines = append(currentBodyLines, line)
			}
			continue
		}

		// When not in a code fence, check for level-1 prompt heading: # #N ...
		if !inFence {
			matches := headingRegex.FindStringSubmatch(line)
			if matches != nil {
				finalizePrompt()

				numInt, _ := strconv.Atoi(matches[1])
				rawTitle := matches[2]
				normTitle := normalizeTitle(rawTitle)

				currentPrompt = &Prompt{
					Number: PromptNumber(numInt),
					Title:  normTitle,
					Line:   lineNum,
				}
				continue
			}
		}

		if currentPrompt != nil {
			currentBodyLines = append(currentBodyLines, line)
		}
	}

	finalizePrompt()
	return prompts, nil
}

// ParseQueueBytes parses a queue from a byte slice.
func ParseQueueBytes(data []byte) ([]Prompt, error) {
	return ParseQueueText(string(data))
}

func isBlankLine(line string) bool {
	for _, r := range line {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func normalizeTitle(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	// Strip single leading colon, hyphen, en-dash, or em-dash
	runes := []rune(t)
	switch runes[0] {
	case ':', '-', '\u2013', '\u2014':
		t = strings.TrimSpace(string(runes[1:]))
	}
	return t
}

func parseFence(line string, inFence bool, openChar byte, openLen int) (isFence bool, fChar byte, fLen int, isCloser bool) {
	// Leading spaces must be <= 3
	leadingSpaces := 0
	for leadingSpaces < len(line) && line[leadingSpaces] == ' ' {
		leadingSpaces++
	}
	if leadingSpaces > 3 {
		return false, 0, 0, false
	}

	rest := line[leadingSpaces:]
	if len(rest) == 0 {
		return false, 0, 0, false
	}

	ch := rest[0]
	if ch != '`' && ch != '~' {
		return false, 0, 0, false
	}

	count := 0
	for count < len(rest) && rest[count] == ch {
		count++
	}
	if count < 3 {
		return false, 0, 0, false
	}

	if inFence {
		if ch == openChar && count >= openLen {
			// A closing fence must only have whitespace after it
			after := rest[count:]
			if strings.TrimSpace(after) == "" {
				return true, ch, count, true
			}
		}
		return false, 0, 0, false
	}

	return true, ch, count, false
}
