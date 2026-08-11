package staticasset

// Asset is one immutable file published from a versioned embedded catalog.
type Asset struct {
	Name        string
	Key         string
	URL         string
	ContentType string
	Data        []byte
}
