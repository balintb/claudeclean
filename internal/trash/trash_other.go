//go:build !darwin

package trash

type unsupported struct{}

// Never falls back to deleting files.
func New() Trasher { return unsupported{} }

func (unsupported) Trash(path string) (string, error) {
	if err := checkPath(path); err != nil {
		return "", err
	}
	return "", ErrUnsupported
}
