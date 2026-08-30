package chinese

import _ "embed"

var (
	//go:embed data/s_1.txt
	simplifiedDictionary string

	//go:embed data/t_1.txt
	traditionalDictionary string

	//go:embed data/stop_tokens.txt
	chineseStopwords string
)
