package multiselect

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
	param string
}

func (ms *MultiSelect) encodeState(st state) string {
	parts := []string{
		strconv.Itoa(st.cmd),
		st.param,
	}
	return ms.prefix + strings.Join(parts, queryDataSeparator)
}

func (ms *MultiSelect) decodeState(queryData string) state {
	parts := strings.SplitN(strings.TrimPrefix(queryData, ms.prefix), queryDataSeparator, 2)

	if len(parts) != 2 {
		panic(fmt.Errorf("invalid data format, expected 2 parts, got %d", len(parts)))
	}

	cmd, err := strconv.Atoi(parts[0])
	if err != nil {
		panic(fmt.Errorf("invalid command: %s", err))
	}

	param := parts[1]

	return state{
		cmd:   cmd,
		param: param,
	}
}
