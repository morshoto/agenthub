//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package prompt

func (s *Session) canUseCursorMenu(label string, options []string, defaultValue string, search bool) bool {
	return false
}

func (s *Session) selectWithCursor(label string, options []string, defaultValue string) (string, error) {
	return "", errCursorMenuUnavailable
}

func (s *Session) selectWithSearchCursor(label string, options []string, defaultValue string) (string, error) {
	return "", errCursorMenuUnavailable
}
