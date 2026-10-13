package dns

type Message struct {
	Header     Header
	Questions  []Question
	Answers    []ResourceRecord
	Authority  []ResourceRecord
	Additional []ResourceRecord
}

type Header struct {
	ID      uint16
	Flags   uint16
	QDCount uint16
	ANCount uint16
	NSCount uint16
	ARCount uint16
}

type Question struct {
	QName  Name
	QType  uint16
	QClass uint16
}

type ResourceRecord struct {
	Name  Name
	Type  uint16
	Class uint16
	TTL   uint32
	RData []byte
}

type Name struct {
	Labels []string
}
