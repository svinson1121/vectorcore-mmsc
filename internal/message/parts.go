package message

type Part struct {
	ContentType     string
	ContentID       string
	ContentLocation string
	Data            []byte
	StorePath       string
	Size            int64
}
