package layout

type Count struct {
	Category string
	Count    int
}

type Info struct {
	Kind          string
	Product       string // "nomad" or "consul"
	Confidence    string
	MetadataFiles []string
	Counts        []Count
	Notes         []string
}
