package agent

type ErrInvalidRequest string

func (e ErrInvalidRequest) Error() string {
	return string(e)
}
