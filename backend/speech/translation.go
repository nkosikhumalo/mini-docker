// Speech translation helper.
// This is a placeholder for multilingual interview support (detect language
// and translate transcript before scoring/LLM evaluation).

package speech

// TranslateToEnglish should translate the transcript to English for consistent
// keyword extraction and LLM evaluation.
//
// Current stub returns the input unchanged.
func TranslateToEnglish(transcript string, sourceLang string) (string, error) {
	return transcript, nil
}
