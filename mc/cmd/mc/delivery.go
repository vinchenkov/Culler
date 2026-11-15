package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"

	"mc/verbs"
)

// deliveryInputLimit bounds a request or a result on standard input.
const deliveryInputLimit = 16 << 20

func cmdDelivery(args []string, stdin io.Reader) (any, error) {
	const usage = "usage: mc delivery register|start|heartbeat|fail|record|limits|allow-corrections"
	if len(args) == 0 {
		return nil, verbs.Usagef(usage)
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "mc delivery"); err != nil {
		return nil, err
	}
	fs := newFlags("mc delivery " + args[0])
	project := fs.String("project", "", "")
	quiet := fs.Int("quiet-seconds", 0, "the destination quiet interval")
	temporary := fs.Int64("max-temporary-bytes", 0, "the temporary repository limit")
	operation := fs.String("operation", "", "")
	run := fs.String("run", "", "")
	result := fs.String("result", "", "")
	request := fs.String("request", "", "")
	condition := fs.String("condition", "", "")
	containerPresent := fs.Bool("container-present", false, "")
	task := fs.Int64("task", 0, "")
	reason := fs.String("reason", "", "")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	switch args[0] {
	case "register":
		if fs.NArg() != 0 {
			return nil, verbs.Usagef("usage: mc delivery register")
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.DeliveryRegister(db, id) })
	case "start":
		if *request == "" || fs.NArg() != 0 {
			return nil, verbs.Usagef("usage: mc delivery start --request -")
		}
		var req verbs.StartRequest
		if err := readDeliveryInput(*request, stdin, &req); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.DeliveryStart(db, id, req) })
	case "heartbeat":
		if *request == "" || fs.NArg() != 0 {
			return nil, verbs.Usagef("usage: mc delivery heartbeat --request -")
		}
		var req verbs.HeartbeatRequest
		if err := readDeliveryInput(*request, stdin, &req); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.DeliveryHeartbeat(db, id, req) })
	case "fail":
		if *run == "" || *condition == "" || fs.NArg() != 0 {
			return nil, verbs.Usagef("usage: mc delivery fail --run <run_id> --condition <text> [--container-present]")
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.DeliveryFail(db, id, *run, *condition, *containerPresent) })
	case "allow-corrections":
		return withSpine(func(db *sql.DB) (any, error) { return verbs.DeliveryAllowance(db, id, *task, *reason) })
	case "limits":
		if *project == "" || fs.NArg() != 0 {
			return nil, verbs.Usagef("usage: mc delivery limits --project <id> [--quiet-seconds <n>] [--max-temporary-bytes <n>]")
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.ConfigureRepositoryLimits(db, id, *project, verbs.RepositoryLimits{QuietSeconds: *quiet, TemporaryBytes: *temporary})
		})
	case "record":
		body := *result
		if body == "-" {
			input, err := readBoundedInput(stdin)
			if err != nil {
				return nil, err
			}
			body = string(input)
		}
		r, err := verbs.RepositoryResultJSON(body)
		if err != nil {
			return nil, verbs.Usagef("the repository result requires JSON: %v", err)
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.RepositoryRecord(db, id, *operation, *run, r) })
	default:
		return nil, verbs.Usagef(usage)
	}
}

// readDeliveryInput decodes a request from its flag value, or from standard
// input when the value is "-".
func readDeliveryInput(value string, stdin io.Reader, into any) error {
	body := []byte(value)
	if value == "-" {
		var err error
		if body, err = readBoundedInput(stdin); err != nil {
			return err
		}
	}
	if err := json.Unmarshal(body, into); err != nil {
		return verbs.Usagef("the delivery request requires JSON: %v", err)
	}
	return nil
}

func readBoundedInput(stdin io.Reader) ([]byte, error) {
	if stdin == nil {
		stdin = os.Stdin
	}
	body, err := io.ReadAll(io.LimitReader(stdin, deliveryInputLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > deliveryInputLimit {
		return nil, verbs.Usagef("the delivery input exceeds its size limit")
	}
	return body, nil
}
