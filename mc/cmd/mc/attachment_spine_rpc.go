package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"mc/verbs"
)

type attachmentSQLValue struct {
	Bool    bool   `json:"bool,omitempty"`
	Bytes   string `json:"bytes,omitempty"`
	Float   string `json:"float,omitempty"`
	Integer string `json:"integer,omitempty"`
	String  string `json:"string,omitempty"`
	Type    string `json:"type"`
}

type attachmentSpineRPCRequest struct {
	Args      []attachmentSQLValue `json:"args,omitempty"`
	Operation string               `json:"operation"`
	Statement string               `json:"statement,omitempty"`
	Version   int                  `json:"version"`
}

type attachmentSpineRPCResponse struct {
	Columns      []string               `json:"columns,omitempty"`
	Error        string                 `json:"error,omitempty"`
	LastInsertID string                 `json:"last_insert_id,omitempty"`
	Rows         [][]attachmentSQLValue `json:"rows,omitempty"`
	RowsAffected string                 `json:"rows_affected,omitempty"`
	Version      int                    `json:"version"`
}

func readPrivateMessage(r io.Reader, value any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > maxPrivateFrameBytes {
		return fmt.Errorf("private message length %d is outside 1..1048576", n)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	if err := json.Unmarshal(body, value); err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if !bytes.Equal(body, canonical) {
		return fmt.Errorf("private message is not the closed canonical encoding")
	}
	return nil
}

func runPrivateAttachmentSpineRPC(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr,
			verbs.Domainf("private attachment spine RPC requires the helper's fixed spine scope"))
	}
	if len(args) != 3 || args[1] != "--deadline-unix-ms" {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("private attachment spine RPC requires its broker deadline"))
	}
	deadlineMillis, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("private attachment spine RPC deadline is invalid"))
	}
	deadlineAt := time.UnixMilli(deadlineMillis)
	remaining, ok := helperDeadlineBudget(deadlineAt, time.Now())
	if !ok {
		return privateHelperTimeoutExitCode
	}
	deadline := time.AfterFunc(remaining, func() {
		os.Exit(privateHelperTimeoutExitCode)
	})
	defer deadline.Stop()

	db, err := verbs.OpenSpine(helperSpinePath())
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	defer db.Close()
	var serve func(*sql.DB, io.Reader, io.Writer) error
	switch args[0] {
	case "__attachment-publisher-spine-rpc":
		serve = servePrivateAttachmentSpineRPC
	case "__attachment-terminal-spine-rpc":
		serve = servePrivateAttachmentTerminalSpineRPC
	default:
		return writeVerbError(stdout, stderr,
			verbs.Usagef("private attachment spine RPC profile is unknown"))
	}
	if err := serve(db, stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "mc: private attachment spine RPC:", err)
		return 2
	}
	return 0
}

func servePrivateAttachmentSpineRPC(
	db *sql.DB,
	input io.Reader,
	output io.Writer,
) error {
	return servePrivateAttachmentSpineRPCProfile(
		db, input, output, "publisher",
		verbs.AttachmentPublicationQueryAllowed,
		verbs.AttachmentPublicationMutationAllowed,
	)
}

func servePrivateAttachmentTerminalSpineRPC(
	db *sql.DB,
	input io.Reader,
	output io.Writer,
) error {
	return servePrivateAttachmentSpineRPCProfile(
		db, input, output, "terminal",
		verbs.AttachmentTerminalQueryAllowed,
		verbs.AttachmentTerminalMutationAllowed,
	)
}

func servePrivateAttachmentSpineRPCProfile(
	db *sql.DB,
	input io.Reader,
	output io.Writer,
	profile string,
	queryAllowed func(string) bool,
	mutationAllowed func(string) bool,
) error {
	var connection *sql.Conn
	defer func() {
		if connection != nil {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
			_ = connection.Close()
		}
	}()
	for {
		var request attachmentSpineRPCRequest
		if err := readPrivateMessage(input, &request); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		response := attachmentSpineRPCResponse{Version: 1}
		if request.Version != 1 {
			response.Error = "attachment spine RPC version mismatch"
		} else {
			switch request.Operation {
			case "begin":
				if connection != nil || request.Statement != "" || len(request.Args) != 0 {
					response.Error = "attachment spine RPC begin is malformed"
					break
				}
				started, err := db.Conn(context.Background())
				if err != nil {
					response.Error = err.Error()
					break
				}
				if _, err := started.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
					_ = started.Close()
					response.Error = err.Error()
					break
				}
				connection = started
			case "commit":
				if connection == nil || request.Statement != "" || len(request.Args) != 0 {
					response.Error = "attachment spine RPC commit is malformed"
					break
				}
				_, err := connection.ExecContext(context.Background(), "COMMIT")
				if err != nil {
					response.Error = err.Error()
					_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
				}
				_ = connection.Close()
				connection = nil
			case "rollback":
				if connection == nil || request.Statement != "" || len(request.Args) != 0 {
					response.Error = "attachment spine RPC rollback is malformed"
					break
				}
				_, err := connection.ExecContext(context.Background(), "ROLLBACK")
				if err != nil {
					response.Error = err.Error()
				}
				_ = connection.Close()
				connection = nil
			case "query":
				if !queryAllowed(request.Statement) {
					response.Error = "attachment spine RPC statement is not in the " +
						profile + " allowlist"
					break
				}
				values, err := decodeAttachmentSQLValues(request.Args)
				if err != nil {
					response.Error = err.Error()
					break
				}
				response = queryAttachmentSpineRPC(db, connection, request.Statement, values)
			case "exec":
				if !mutationAllowed(request.Statement) {
					response.Error = "attachment spine RPC statement is not in the " +
						profile + " allowlist"
					break
				}
				if connection == nil {
					response.Error = "attachment spine RPC mutation requires BEGIN IMMEDIATE"
					break
				}
				values, err := decodeAttachmentSQLValues(request.Args)
				if err != nil {
					response.Error = err.Error()
					break
				}
				response = execAttachmentSpineRPC(connection, request.Statement, values)
			case "close":
				if request.Statement != "" || len(request.Args) != 0 {
					response.Error = "attachment spine RPC close is malformed"
				} else if connection != nil {
					_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
					_ = connection.Close()
					connection = nil
				}
				if err := writePrivateFrame(output, response); err != nil {
					return err
				}
				return nil
			default:
				response.Error = "attachment spine RPC operation is unknown"
			}
		}
		if err := writePrivateFrame(output, response); err != nil {
			return err
		}
	}
}

type attachmentSQLQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryAttachmentSpineRPC(
	db *sql.DB,
	connection *sql.Conn,
	statement string,
	args []any,
) attachmentSpineRPCResponse {
	response := attachmentSpineRPCResponse{Version: 1}
	var queryer attachmentSQLQueryer = db
	if connection != nil {
		queryer = connection
	}
	rows, err := queryer.QueryContext(context.Background(), statement, args...)
	if err != nil {
		response.Error = err.Error()
		return response
	}
	defer rows.Close()
	response.Columns, err = rows.Columns()
	if err != nil {
		response.Error = err.Error()
		return response
	}
	for rows.Next() {
		values := make([]any, len(response.Columns))
		destinations := make([]any, len(values))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			response.Error = err.Error()
			return response
		}
		encoded, err := encodeAttachmentSQLValues(values)
		if err != nil {
			response.Error = err.Error()
			return response
		}
		response.Rows = append(response.Rows, encoded)
	}
	if err := rows.Err(); err != nil {
		response.Error = err.Error()
	}
	return response
}

func execAttachmentSpineRPC(
	connection *sql.Conn,
	statement string,
	args []any,
) attachmentSpineRPCResponse {
	response := attachmentSpineRPCResponse{Version: 1}
	result, err := connection.ExecContext(context.Background(), statement, args...)
	if err != nil {
		response.Error = err.Error()
		return response
	}
	affected, err := result.RowsAffected()
	if err != nil {
		response.Error = err.Error()
		return response
	}
	response.RowsAffected = strconv.FormatInt(affected, 10)
	if inserted, err := result.LastInsertId(); err == nil {
		response.LastInsertID = strconv.FormatInt(inserted, 10)
	}
	return response
}

func encodeAttachmentSQLValues(values []any) ([]attachmentSQLValue, error) {
	encoded := make([]attachmentSQLValue, 0, len(values))
	for _, value := range values {
		item := attachmentSQLValue{}
		switch value := value.(type) {
		case nil:
			item.Type = "null"
		case string:
			item.Type, item.String = "string", value
		case []byte:
			item.Type, item.Bytes = "bytes", base64.StdEncoding.EncodeToString(value)
		case int64:
			item.Type, item.Integer = "integer", strconv.FormatInt(value, 10)
		case float64:
			item.Type, item.Float = "float", strconv.FormatFloat(value, 'g', -1, 64)
		case bool:
			item.Type, item.Bool = "bool", value
		default:
			return nil, fmt.Errorf("attachment spine RPC cannot encode SQL value %T", value)
		}
		encoded = append(encoded, item)
	}
	return encoded, nil
}

func decodeAttachmentSQLValues(values []attachmentSQLValue) ([]any, error) {
	decoded := make([]any, 0, len(values))
	for _, value := range values {
		switch value.Type {
		case "null":
			if value.Bool || value.Bytes != "" || value.Float != "" ||
				value.Integer != "" || value.String != "" {
				return nil, fmt.Errorf("attachment spine RPC null value is expanded")
			}
			decoded = append(decoded, nil)
		case "string":
			if value.Bool || value.Bytes != "" || value.Float != "" || value.Integer != "" {
				return nil, fmt.Errorf("attachment spine RPC string value is expanded")
			}
			decoded = append(decoded, value.String)
		case "bytes":
			if value.Bool || value.Float != "" || value.Integer != "" || value.String != "" {
				return nil, fmt.Errorf("attachment spine RPC bytes value is expanded")
			}
			body, err := base64.StdEncoding.Strict().DecodeString(value.Bytes)
			if err != nil {
				return nil, fmt.Errorf("attachment spine RPC bytes value is malformed")
			}
			decoded = append(decoded, body)
		case "integer":
			if value.Bool || value.Bytes != "" || value.Float != "" || value.String != "" {
				return nil, fmt.Errorf("attachment spine RPC integer value is expanded")
			}
			number, err := strconv.ParseInt(value.Integer, 10, 64)
			if err != nil || strconv.FormatInt(number, 10) != value.Integer {
				return nil, fmt.Errorf("attachment spine RPC integer value is malformed")
			}
			decoded = append(decoded, number)
		case "float":
			if value.Bool || value.Bytes != "" || value.Integer != "" || value.String != "" {
				return nil, fmt.Errorf("attachment spine RPC float value is expanded")
			}
			number, err := strconv.ParseFloat(value.Float, 64)
			if err != nil || strconv.FormatFloat(number, 'g', -1, 64) != value.Float {
				return nil, fmt.Errorf("attachment spine RPC float value is malformed")
			}
			decoded = append(decoded, number)
		case "bool":
			if value.Bytes != "" || value.Float != "" || value.Integer != "" || value.String != "" {
				return nil, fmt.Errorf("attachment spine RPC bool value is expanded")
			}
			decoded = append(decoded, value.Bool)
		default:
			return nil, fmt.Errorf("attachment spine RPC SQL value type is unknown")
		}
	}
	return decoded, nil
}
