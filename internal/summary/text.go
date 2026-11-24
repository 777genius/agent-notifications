package summary

import (
	"strings"
)

// Helper functions

func extractFirstSentence(text string) string {
	// Find first sentence (ending with . ! or ?)
	// If first sentence is too short (< 20 chars), try to include second sentence too
	const minSentenceLength = 20
	const maxLength = 200

	var sentences []string
	var currentStart int
	runes := []rune(text)

	for i, char := range runes {
		if char == '.' || char == '!' || char == '?' {
			// For dots, check if this is really end of sentence:
			// - Must be followed by space + uppercase letter, or end of string
			// - Should not be preceded by a digit (to avoid "v1.6.0")
			if char == '.' {
				// Check if preceded by digit (version numbers like v1.6.0)
				if i > 0 && runes[i-1] >= '0' && runes[i-1] <= '9' {
					continue
				}
				// Check if followed by digit (decimal numbers like 1.5)
				if i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9' {
					continue
				}
				// Check if followed by letter without space (abbreviations, domains)
				if i+1 < len(runes) && runes[i+1] != ' ' && runes[i+1] != '\n' {
					continue
				}
			}

			// Include punctuation in the sentence
			sentence := strings.TrimSpace(string(runes[currentStart : i+1]))
			if sentence != "" {
				sentences = append(sentences, sentence)
				currentStart = i + 1

				// Calculate total length so far
				totalLength := len(strings.Join(sentences, " "))

				// If we have at least one sentence and either:
				// 1. Total length >= minSentenceLength, OR
				// 2. Total length >= maxLength
				// Then return what we have
				if len(sentences) == 1 && totalLength < minSentenceLength && totalLength < maxLength {
					// First sentence too short, continue to get second
					continue
				}

				if totalLength >= maxLength {
					// Too long, return what we had before last sentence
					if len(sentences) > 1 {
						return strings.Join(sentences[:len(sentences)-1], " ")
					}
				}

				// Good length, return
				return strings.Join(sentences, " ")
			}
		}
	}

	// No sentence ending found
	if len(sentences) > 0 {
		return strings.Join(sentences, " ")
	}

	// Return first 100 chars if no punctuation found
	if len(runes) > 100 {
		return string(runes[:100])
	}
	return text
}

func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}

	// Step 1: Try to find sentence boundary (., !, ?) within maxLen
	// Look for the last sentence-ending punctuation in the allowed range
	// Use runes to avoid cutting in the middle of a multi-byte character
	searchText := string(runes[:maxLen])

	// Check for sentence enders: ". ", "! ", "? " (followed by space or newline)
	// Also check for end of string within maxLen
	lastSentenceEnd := -1

	// Try sentence endings with space/newline after
	// Find the FIRST suitable sentence ending (to avoid partial next sentences)
	sentenceEnders := []string{". ", "! ", "? ", ".\n", "!\n", "?\n"}
	for _, ender := range sentenceEnders {
		idx := 0
		for {
			pos := strings.Index(searchText[idx:], ender)
			if pos < 0 {
				break
			}
			actualPos := idx + pos
			// Check if this position is suitable: not too early
			if actualPos > maxLen/3 {
				// Found a suitable sentence ending
				// Only use it if we haven't found one yet, or this is a better one
				// (we want the FIRST suitable one, not the last)
				if lastSentenceEnd < 0 {
					lastSentenceEnd = actualPos
					break // Stop searching for this ender
				}
			}
			idx = actualPos + 1
		}
		if lastSentenceEnd >= 0 {
			break // Found a suitable sentence, no need to check other enders
		}
	}

	// Also try sentence ending at the very end of searchText (no space after)
	if lastSentenceEnd < 0 && len(searchText) > 0 {
		lastChar := searchText[len(searchText)-1]
		if lastChar == '.' || lastChar == '!' || lastChar == '?' {
			lastSentenceEnd = len(searchText) - 1
		}
	}

	if lastSentenceEnd >= 0 {
		// Found a sentence boundary, truncate there (including the punctuation)
		return strings.TrimSpace(searchText[:lastSentenceEnd+1])
	}

	// Step 2: No sentence boundary found, try word boundary
	// Still use runes to be safe
	truncatedRunes := runes[:maxLen-3]
	truncated := string(truncatedRunes)
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}
