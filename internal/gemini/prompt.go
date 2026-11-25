package gemini

// Prompt is the transcription instruction. Each rule is stated once: the
// earlier prompt repeated the same restriction three or four ways ("do not
// invent", "do not add", "do not introduce", "do not reconstruct"), and that
// stacking made Gemini refuse more often. Kept rules each answer a failure
// seen in practice: paragraphs (a single wall of text), starting with the
// first spoken words (the model wrote its plan, a timestamped draft or a
// slide outline into the answer), documents only for terms (RECITATION), and
// LaTeX for spoken math. This file is the source of truth.
const Prompt = `Transcribe this lecture audio (Korean/English mixed) verbatim.

- Keep the speaker's wording, tone, repetitions and code-switching as spoken, for example "이 domain과 codomain의 차이를 잘 아셔야 해요". Fix only spacing and punctuation.
- Correct a misheard technical term only when the pronunciation supports it. Mark unintelligible speech as [불명확].
- Attached documents only help recognize terms. Write only what is spoken.
- Write math the speaker reads out in LaTeX: $...$ inline, $$...$$ on its own line.
- Begin with the first spoken words and output only the transcript.
- Separate paragraphs with one blank line. Start a new paragraph when the topic or example changes, or every 3–6 sentences; do not break after every sentence.`

// LivePrompt is Prompt for a request that carries more than one audio part.
const LivePrompt = Prompt + `

The audio parts are in chronological order. Transcribe them as one continuous transcript.`

// PromptFor is the system instruction for a request with this many audio
// parts: LivePrompt when there is more than one, Prompt otherwise.
func PromptFor(audioParts int) string {
	if audioParts > 1 {
		return LivePrompt
	}
	return Prompt
}
