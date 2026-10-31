package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var headingValRegex = regexp.MustCompile(`^#[ \t]+#([^\s:]+)(?:[:\-\x{2013}\x{2014}]?[ \t]*(.*?))?[ \t]*$`)

// QueueValidationError holds one or more validation failures found in a queue file.
type QueueValidationError struct {
	Errors []string
}

func (e *QueueValidationError) Error() string {
	return strings.Join(e.Errors, "\n")
}

// ValidateQueueBytes parses and validates a Markdown queue byte slice per docs/07 §5.
func ValidateQueueBytes(data []byte) ([]Prompt, error) {
	return ValidateQueueText(string(data))
}

// ValidateQueueText parses and validates a Markdown queue string per docs/07 §5.
// It collects all problems and returns them in a QueueValidationError.
func ValidateQueueText(content string) ([]Prompt, error) {
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	lines := strings.Split(content, "\n")

	var prompts []Prompt
	var errs []string

	var currentPrompt *Prompt
	var currentBodyLines []string
	currentHeadingLine := 0

	inFence := false
	fenceChar := byte(0)
	fenceLen := 0
	fenceStartLine := 0

	seenNumbers := make(map[PromptNumber]int) // number -> line first seen
	var prevNumber PromptNumber

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

		if strings.TrimSpace(body) == "" {
			errs = append(errs, fmt.Sprintf("line %d: prompt #%d has an empty body", currentHeadingLine, currentPrompt.Number))
		}

		if start < len(currentBodyLines) {
			if strings.TrimSpace(currentBodyLines[start]) == "<!-- agyq:skip -->" {
				currentPrompt.Skip = true
			}
		}

		currentPrompt.Body = body
		currentPrompt.Hash = ComputePromptHash(currentPrompt.Title, body)
		prompts = append(prompts, *currentPrompt)
		currentPrompt = nil
		currentBodyLines = nil
		currentHeadingLine = 0
	}

	for i, line := range lines {
		lineNum := i + 1

		isFence, fChar, fLen, isCloser := parseFence(line, inFence, fenceChar, fenceLen)
		if isFence {
			if !inFence {
				inFence = true
				fenceChar = fChar
				fenceLen = fLen
				fenceStartLine = lineNum
			} else if isCloser {
				inFence = false
				fenceChar = 0
				fenceLen = 0
				fenceStartLine = 0
			}
			if currentPrompt != nil {
				currentBodyLines = append(currentBodyLines, line)
			}
			continue
		}

		if !inFence {
			matches := headingValRegex.FindStringSubmatch(line)
			if matches != nil {
				finalizePrompt()

				rawNum := matches[1]
				numInt, err := strconv.Atoi(rawNum)
				if err != nil || numInt < 1 || numInt > 999999 {
					errs = append(errs, fmt.Sprintf("line %d: invalid prompt number %q (must be 1–999999)", lineNum, rawNum))
					continue
				}

				pNum := PromptNumber(numInt)
				if firstLine, exists := seenNumbers[pNum]; exists {
					errs = append(errs, fmt.Sprintf("line %d: duplicate prompt number #%d (first defined on line %d)", lineNum, numInt, firstLine))
				} else {
					seenNumbers[pNum] = lineNum
				}

				if prevNumber > 0 && pNum <= prevNumber {
					errs = append(errs, fmt.Sprintf("line %d: prompt #%d is out of order (previous was #%d); numbers must be strictly ascending", lineNum, numInt, prevNumber))
				}
				prevNumber = pNum

				normTitle := normalizeTitle(matches[2])
				currentHeadingLine = lineNum
				currentPrompt = &Prompt{
					Number: pNum,
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

	if inFence {
		errs = append(errs, fmt.Sprintf("line %d: unterminated code fence started on this line swallows the rest of the file", fenceStartLine))
	}

	if len(prompts) == 0 && len(errs) == 0 {
		errs = append(errs, "no prompts found: expected headings like \"# #1 Title\"")
	}

	if len(errs) > 0 {
		return prompts, &QueueValidationError{Errors: errs}
	}
	return prompts, nil
}

// ValidatePrompts checks that an already-parsed slice of prompts is valid per docs/07 §5.
func ValidatePrompts(prompts []Prompt) error {
	var errs []string
	seen := make(map[PromptNumber]bool)
	var prev PromptNumber
	for _, p := range prompts {
		if p.Number < 1 || p.Number > 999999 {
			errs = append(errs, fmt.Sprintf("invalid prompt number #%d (must be 1–999999)", p.Number))
		}
		if seen[p.Number] {
			errs = append(errs, fmt.Sprintf("duplicate prompt number #%d", p.Number))
		}
		seen[p.Number] = true
		if prev > 0 && p.Number <= prev {
			errs = append(errs, fmt.Sprintf("prompt #%d is out of order (previous was #%d)", p.Number, prev))
		}
		prev = p.Number
		if strings.TrimSpace(p.Body) == "" {
			errs = append(errs, fmt.Sprintf("prompt #%d has an empty body", p.Number))
		}
	}
	if len(prompts) == 0 {
		errs = append(errs, "no prompts found")
	}
	if len(errs) > 0 {
		return &QueueValidationError{Errors: errs}
	}
	return nil
}
