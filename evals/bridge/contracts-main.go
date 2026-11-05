package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mc/verbs"
	"os"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err == nil {
		var result any
		result, err = verbs.EvalContract(data)
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(result)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
