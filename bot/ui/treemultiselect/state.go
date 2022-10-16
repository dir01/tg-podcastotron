package treemultiselect

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	queryDataSeparator = ":"
)

type state struct {
	cmd   int
	param int
}

func (tms *TreeMultiSelect) encodeState(st state) string {
	parts := []string{
		strconv.Itoa(st.cmd),
		strconv.Itoa(st.param),
	}
	return tms.prefix + strings.Join(parts, queryDataSeparator)
}

func (tms *TreeMultiSelect) decodeState(queryData string) state {
	parts := strings.SplitN(strings.TrimPrefix(queryData, tms.prefix), queryDataSeparator, 2)

	if len(parts) != 2 {
		panic(fmt.Errorf("invalid data format, expected 2 parts, got %d", len(parts)))
	}

	cmd, err := strconv.Atoi(parts[0])
	if err != nil {
		panic(fmt.Errorf("invalid command: %s", err))
	}

	param, err := strconv.Atoi(parts[1])
	if err != nil {
		panic(fmt.Errorf("invalid param: %s", err))
	}

	return state{
		cmd:   cmd,
		param: param,
	}
}
