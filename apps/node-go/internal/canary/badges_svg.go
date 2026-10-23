package canary

import "fmt"

// renderStatusBadge draws the design/badges.svg badge for one public state.
// Every byte comes from the fixed tokens below, never from request text; an
// unrecognized state renders as unknown. textLength pins each word to a fixed
// advance so the layout cannot overflow with whichever system font renders it.
// Only the outer corners are rounded, so segments join without notches.
func renderStatusBadge(state string, maintenance bool) string {
	// Unknown: dashed ring with a question mark, neutral.
	width, color, words, glyph := 74, "#5c5b57", 47, `<circle cx="69" cy="10" r="4.8" fill="none" stroke="#fff" stroke-width="1.3" stroke-dasharray="1.9 1.4"/><path d="M67.7 8.6a1.4 1.4 0 1 1 2 1.2c-.45.25-.7.5-.7.9" fill="none" stroke="#fff" stroke-width="1.1" stroke-linecap="round"/><circle cx="69" cy="12.5" r=".7" fill="#fff"/>`
	switch state {
	case "up": // check in a filled circle
		width, color, words = 40, "#0b7a0b", 13
		glyph = `<circle cx="69" cy="10" r="5.5" fill="#fff"/><path d="M66.6 10.2l1.7 1.7 3.1-3.4" fill="none" stroke="#0b7a0b" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>`
	case "down": // cross in a rounded square
		width, color, words = 55, "#b02f2f", 28
		glyph = `<rect x="64" y="5" width="10" height="10" rx="2.2" fill="#fff"/><path d="M66.8 7.8l4.4 4.4M71.2 7.8l-4.4 4.4" stroke="#b02f2f" stroke-width="1.5" stroke-linecap="round"/>`
	case "pending": // clock ring
		width, words = 69, 42
		glyph = `<circle cx="69" cy="10" r="4.8" fill="none" stroke="#fff" stroke-width="1.3"/><path d="M69 7.4V10l1.8 1.2" fill="none" stroke="#fff" stroke-width="1.3" stroke-linecap="round"/>`
	default:
		state = "unknown"
	}
	title, total := "status: "+state, 58+width
	// The state segment keeps square right corners when maintenance follows it.
	segment := fmt.Sprintf(`<path d="M58 0h%dv20H58z" fill="%s"/>`, width, color)
	extra := ""
	if maintenance {
		title += ", maintenance"
		extra = fmt.Sprintf(`<path d="M%d 0h86a4 4 0 0 1 4 4v12a4 4 0 0 1-4 4h-86z" fill="#fab219"/><g transform="translate(%d 0)"><path d="M11.6 6.2a2.6 2.6 0 0 0-3.1 3.4l-3 3a1.1 1.1 0 0 0 1.6 1.6l3-3a2.6 2.6 0 0 0 3.4-3.1l-1.5 1.5-1.4-.3-.3-1.4z" fill="#151514"/><text class="f" x="18" y="14" textLength="65" fill="#151514">maintenance</text></g>`, total, total)
		total += 90
	} else {
		segment = fmt.Sprintf(`<path d="M58 0h%da4 4 0 0 1 4 4v12a4 4 0 0 1-4 4h-%dz" fill="%s"/>`, width-4, width-4, color)
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" viewBox="0 0 %d 20" role="img" aria-labelledby="title"><title id="title">%s</title><style>.f{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Noto Sans',Helvetica,Arial,sans-serif;font-size:11px;font-weight:600}</style><path d="M4 0h54v20H4a4 4 0 0 1-4-4V4a4 4 0 0 1 4-4z" fill="#151514"/><g transform="translate(5 4)"><path d="M10.4 4.5C10.4 3.1 9.4 2 8.1 2C6.9 2 5.9 3 5.9 4.3C5.9 4.9 6 5.3 6.3 5.7C4.5 6.1 3.2 7.5 2.8 9.2L1.3 10.2L3.5 10.2C4.3 10.6 5.2 10.8 6.2 10.8C8.7 10.8 10.7 9.1 10.7 6.8C10.7 5.9 10.6 5.1 10.4 4.5Z" fill="#ffd23f"/><path d="M10.3 3.8L12.2 4.5L10.4 5.2Z" fill="#f28c28"/><circle cx="8.8" cy="3.9" r=".55" fill="#151514"/></g><text class="f" x="21" y="14" textLength="31" fill="#f0efec">status</text>%s%s<text class="f" x="78" y="14" textLength="%d" fill="#fff">%s</text>%s</svg>`, total, total, title, segment, glyph, words, state, extra)
}
