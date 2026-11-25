package runner

// Answer checks shared by live requests and Batch results: usable text,
// cut-off answers, blocked prompts.

import (
	"fmt"
	"strings"

	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

// usageLog summarizes a unit's token counts for the log.
func usageLog(u store.Usage) string {
	if u.Empty() {
		return "no usage"
	}
	return fmt.Sprintf("output %d, thoughts %d, audio %d", u.Output, u.Thoughts, u.Audio)
}

// retryDetail is the item detail while a busy server is retried.
func retryDetail() string { return i18n.T("status.retrying") }

// blockedMessage is what the user sees when Gemini refuses the audio. The
// server's reason goes to the log instead.
func blockedMessage() string { return i18n.T("error.blocked") }

// blockedError carries the server's block reason out of checkText.
type blockedError struct{ reason string }

func (e blockedError) Error() string {
	if e.reason == "" {
		return blockedMessage()
	}
	return blockedMessage() + " (" + e.reason + ")"
}

// keepText is what a unit stores from an answer checkText accepted. An
// answer cut off at the output cap keeps what came back plus a line saying
// the rest of that part is missing: the head of a lecture beats nothing, and
// the line keeps the gap visible to the reader and to any tool fed the notes.
func keepText(text, finish string) string {
	text = strings.TrimSpace(text)
	if finish == gemini.FinishMaxTokens {
		text += "\n\n" + i18n.T("transcript.truncated")
	}
	return text
}

func checkText(text, finish string) error {
	if strings.HasPrefix(finish, gemini.FinishBlocked) {
		return blockedError{reason: strings.TrimPrefix(finish, gemini.FinishBlocked)}
	}
	if finish == gemini.FinishMaxTokens {
		// Cut off at the output cap: what came back is kept (see keepText).
		if strings.TrimSpace(text) == "" {
			return userError{i18n.Error("error.truncated")}
		}
		return nil
	}
	if strings.TrimSpace(text) == "" {
		if finish != "" && finish != gemini.FinishStop {
			return userError{i18n.Error("error.emptyResultReason", "reason", finish)}
		}
		return userError{i18n.Error("error.emptyResult")}
	}
	if finish != "" && finish != gemini.FinishStop {
		return userError{i18n.Error("error.stopped", "reason", finish)}
	}
	return nil
}
