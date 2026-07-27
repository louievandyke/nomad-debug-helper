package layout

type Count struct {
	Category string
	Count    int
}

type Info struct {
	Kind          string
	Confidence    string
	MetadataFiles []string
	Counts        []Count
	Notes         []string
}
