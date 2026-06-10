package raised

var (
	ErrValidation  = NewSentinel("failed validation")
	ErrInvalidHash = NewSentinel("invalid hash function")
)
