package files

func ptr[T any](v T) *T {
	return &v
}
