package main

import (
	"fmt"
	"log"
	"strings"
	"unicode/utf8"
)

const MaxPromptLength = 4000

var injectionPatterns = []string{
	"ignore all previous instructions",
	"ignore your system prompt",
	"disregard all prior",
	"new persona",
}

func validatePrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		log.Printf("Rejected prompt: empty")
		return fmt.Errorf("text field cannot be empty")
	}

	charCount := utf8.RuneCountInString(prompt)

	if charCount > MaxPromptLength {
		log.Printf("Rejected prompt: length=%d", charCount)
		return fmt.Errorf(
			"text exceeds maximum length of %d characters (received %d)",
			MaxPromptLength,
			charCount,
		)
	}

	lower := strings.ToLower(prompt)
	for _, pattern := range injectionPatterns {
		if strings.Contains(lower, pattern) {
			log.Printf("Rejected prompt: matched injection pattern %q", pattern)
			return fmt.Errorf("prompt contains disallowed content")
		}
	}

	return nil
}
