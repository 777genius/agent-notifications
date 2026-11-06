package summary

import (
	"strings"
)

// CleanMarkdown cleans markdown formatting from text
// Removes all markdown syntax while preserving the actual text content
func CleanMarkdown(text string) string {
	// Step 1: Remove code blocks first (they can contain markdown-like syntax)
	text = codeBlockPattern.ReplaceAllString(text, "")

	// Step 2: Convert images to alt text (must be before links since images are ![](url))
	text = imagePattern.ReplaceAllString(text, "$1")

	// Step 3: Convert links to text only
	text = linkPattern.ReplaceAllString(text, "$1")

	// Step 4: Remove strikethrough
	text = strikethroughPattern.ReplaceAllString(text, "$1")

	// Step 5: Remove bold (both ** and __)
	text = boldPattern.ReplaceAllString(text, "$2")

	// Step 6: Remove italic (both * and _)
	// Need to be careful with edge cases
	text = italicPattern.ReplaceAllString(text, "$2")

	// Step 7: Remove backticks (inline code)
	text = backtickPattern.ReplaceAllString(text, "")

	// Step 8: Process line by line for line-based patterns
	lines := strings.Split(text, "\n")
	var cleaned []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Remove headers (# text)
		line = headerPattern.ReplaceAllString(line, "")

		// Remove blockquotes (> text)
		line = blockquotePattern.ReplaceAllString(line, "")

		// Remove bullet points (- text, * text, • text)
		line = bulletPattern.ReplaceAllString(line, "")

		// Trim again
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}

	// Step 9: Join lines and normalize whitespace
	result := strings.Join(cleaned, " ")
	result = multiSpacePattern.ReplaceAllString(result, " ")

	return strings.TrimSpace(result)
}
