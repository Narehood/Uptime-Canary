package delivery

// RenderTextTemplate substitutes once, without HTML or implicit fallback. It
// is used for a provider's explicit custom title/body after escaping values.
func RenderTextTemplate(source string, message Message, limit int) (string, error) {
	if limit < 0 || limit > MaxBodyBytes || !body(source, MaxBodyBytes) {
		return "", errMessage
	}
	return renderTemplate(source, message, false, limit)
}

// ValidateTemplates keeps channel admission consistent across adapters,
// including Discord, whose native transport is owned by the Canary node.
func ValidateTemplates(value Templates) bool {
	return header(value.Subject, 512) && body(value.Text, MaxBodyBytes) && body(value.HTML, MaxBodyBytes)
}
