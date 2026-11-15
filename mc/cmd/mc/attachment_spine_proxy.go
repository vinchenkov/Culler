package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

type attachmentRPCChannel interface {
	Exchange(context.Context, attachmentSpineRPCRequest) (attachmentSpineRPCResponse, error)
	Close() error
}

type attachmentSpineProxyDriver struct {
	dial func() (attachmentRPCChannel, error)
}

func (d *attachmentSpineProxyDriver) Open(string) (driver.Conn, error) {
	channel, err := d.dial()
	if err != nil {
		return nil, attachmentProxyInfrastructure(err)
	}
	return &attachmentSpineProxyConn{channel: channel}, nil
}

func openPrivateAttachmentSpineProxy() (*sql.DB, error) {
	return openAttachmentSpineProxy(dialPrivateAttachmentSpineRPC)
}

func openPrivateAttachmentTerminalSpineProxy() (*sql.DB, error) {
	return openAttachmentSpineProxy(dialPrivateAttachmentTerminalSpineRPC)
}

func openAttachmentSpineProxy(
	dial func() (attachmentRPCChannel, error),
) (*sql.DB, error) {
	db := sql.OpenDB(&attachmentSpineProxyConnector{dial: dial})
	// A connection is one bounded helper process. With no idle pool, byte
	// streaming happens with no helper process or SQLite handle left open;
	// an explicit transaction retains exactly one connection until commit.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	return db, nil
}

type attachmentSpineProxyConnector struct {
	dial func() (attachmentRPCChannel, error)
}

func (c *attachmentSpineProxyConnector) Connect(context.Context) (driver.Conn, error) {
	channel, err := c.dial()
	if err != nil {
		return nil, attachmentProxyInfrastructure(err)
	}
	return &attachmentSpineProxyConn{channel: channel}, nil
}

func (c *attachmentSpineProxyConnector) Driver() driver.Driver {
	return &attachmentSpineProxyDriver{dial: c.dial}
}

type attachmentSpineProxyConn struct {
	channel attachmentRPCChannel
	closed  bool
}

func (c *attachmentSpineProxyConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("attachment spine proxy does not prepare statements")
}

func (c *attachmentSpineProxyConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	_, exchangeErr := c.channel.Exchange(context.Background(), attachmentSpineRPCRequest{
		Operation: "close",
		Version:   1,
	})
	closeErr := c.channel.Close()
	return errors.Join(exchangeErr, closeErr)
}

func (c *attachmentSpineProxyConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *attachmentSpineProxyConn) BeginTx(
	ctx context.Context,
	options driver.TxOptions,
) (driver.Tx, error) {
	if options.ReadOnly || options.Isolation != driver.IsolationLevel(sql.LevelDefault) {
		return nil, errors.New("attachment spine proxy supports only the default write transaction")
	}
	if err := c.exchangeEmpty(ctx, "begin"); err != nil {
		return nil, err
	}
	return &attachmentSpineProxyTx{connection: c}, nil
}

func (c *attachmentSpineProxyConn) ExecContext(
	ctx context.Context,
	statement string,
	args []driver.NamedValue,
) (driver.Result, error) {
	switch statement {
	case "BEGIN IMMEDIATE":
		if len(args) != 0 {
			return nil, errors.New("attachment spine proxy transaction control carries values")
		}
		if err := c.exchangeEmpty(ctx, "begin"); err != nil {
			return nil, err
		}
		return attachmentSpineProxyResult{}, nil
	case "COMMIT":
		if len(args) != 0 {
			return nil, errors.New("attachment spine proxy transaction control carries values")
		}
		if err := c.exchangeEmpty(ctx, "commit"); err != nil {
			return nil, err
		}
		return attachmentSpineProxyResult{}, nil
	case "ROLLBACK":
		if len(args) != 0 {
			return nil, errors.New("attachment spine proxy transaction control carries values")
		}
		if err := c.exchangeEmpty(ctx, "rollback"); err != nil {
			return nil, err
		}
		return attachmentSpineProxyResult{}, nil
	}
	values, err := attachmentNamedValues(args)
	if err != nil {
		return nil, err
	}
	response, err := c.channel.Exchange(ctx, attachmentSpineRPCRequest{
		Args: values, Operation: "exec", Statement: statement, Version: 1,
	})
	if err != nil {
		return nil, attachmentProxyInfrastructure(err)
	}
	if err := attachmentRPCResponseError(response); err != nil {
		return nil, err
	}
	affected, err := strconv.ParseInt(response.RowsAffected, 10, 64)
	if err != nil || strconv.FormatInt(affected, 10) != response.RowsAffected {
		return nil, attachmentProxyInfrastructure(
			errors.New("attachment spine proxy received malformed affected-row count"))
	}
	inserted := int64(0)
	if response.LastInsertID != "" {
		inserted, err = strconv.ParseInt(response.LastInsertID, 10, 64)
		if err != nil || strconv.FormatInt(inserted, 10) != response.LastInsertID {
			return nil, attachmentProxyInfrastructure(
				errors.New("attachment spine proxy received malformed insert identity"))
		}
	}
	return attachmentSpineProxyResult{inserted: inserted, affected: affected}, nil
}

func (c *attachmentSpineProxyConn) QueryContext(
	ctx context.Context,
	statement string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	values, err := attachmentNamedValues(args)
	if err != nil {
		return nil, err
	}
	response, err := c.channel.Exchange(ctx, attachmentSpineRPCRequest{
		Args: values, Operation: "query", Statement: statement, Version: 1,
	})
	if err != nil {
		return nil, attachmentProxyInfrastructure(err)
	}
	if err := attachmentRPCResponseError(response); err != nil {
		return nil, err
	}
	rows := make([][]driver.Value, 0, len(response.Rows))
	for _, encoded := range response.Rows {
		if len(encoded) != len(response.Columns) {
			return nil, attachmentProxyInfrastructure(
				errors.New("attachment spine proxy received a ragged query row"))
		}
		decoded, err := decodeAttachmentSQLValues(encoded)
		if err != nil {
			return nil, attachmentProxyInfrastructure(err)
		}
		row := make([]driver.Value, len(decoded))
		for index, value := range decoded {
			switch value.(type) {
			case nil, string, []byte, int64, float64, bool:
				row[index] = value
			default:
				return nil, attachmentProxyInfrastructure(
					fmt.Errorf("attachment spine proxy received unsupported value %T", value))
			}
		}
		rows = append(rows, row)
	}
	return &attachmentSpineProxyRows{columns: response.Columns, rows: rows}, nil
}

func (c *attachmentSpineProxyConn) exchangeEmpty(ctx context.Context, operation string) error {
	response, err := c.channel.Exchange(ctx, attachmentSpineRPCRequest{
		Operation: operation,
		Version:   1,
	})
	if err != nil {
		return attachmentProxyInfrastructure(err)
	}
	return attachmentRPCResponseError(response)
}

type attachmentSpineProxyTx struct {
	connection *attachmentSpineProxyConn
}

func (t *attachmentSpineProxyTx) Commit() error {
	return t.connection.exchangeEmpty(context.Background(), "commit")
}

func (t *attachmentSpineProxyTx) Rollback() error {
	return t.connection.exchangeEmpty(context.Background(), "rollback")
}

type attachmentSpineProxyResult struct {
	inserted int64
	affected int64
}

func (r attachmentSpineProxyResult) LastInsertId() (int64, error) {
	return r.inserted, nil
}

func (r attachmentSpineProxyResult) RowsAffected() (int64, error) {
	return r.affected, nil
}

type attachmentSpineProxyRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *attachmentSpineProxyRows) Columns() []string {
	return r.columns
}

func (r *attachmentSpineProxyRows) Close() error {
	r.index = len(r.rows)
	return nil
}

func (r *attachmentSpineProxyRows) Next(destination []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	if len(destination) != len(r.rows[r.index]) {
		return errors.New("attachment spine proxy row destination has the wrong width")
	}
	copy(destination, r.rows[r.index])
	r.index++
	return nil
}

func attachmentNamedValues(values []driver.NamedValue) ([]attachmentSQLValue, error) {
	raw := make([]any, 0, len(values))
	for index, value := range values {
		if value.Name != "" || value.Ordinal != index+1 {
			return nil, errors.New("attachment spine proxy accepts only ordered positional values")
		}
		raw = append(raw, value.Value)
	}
	return encodeAttachmentSQLValues(raw)
}

func attachmentRPCResponseError(response attachmentSpineRPCResponse) error {
	if response.Version != 1 {
		return attachmentProxyInfrastructure(
			errors.New("attachment spine proxy response version mismatch"))
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

type attachmentSpineProxyInfrastructureError struct {
	cause error
}

func (e *attachmentSpineProxyInfrastructureError) Error() string {
	return e.cause.Error()
}

func (e *attachmentSpineProxyInfrastructureError) Unwrap() error {
	return e.cause
}

func (*attachmentSpineProxyInfrastructureError) SpineInfrastructureFailure() {}

func attachmentProxyInfrastructure(err error) error {
	if err == nil {
		return nil
	}
	return &attachmentSpineProxyInfrastructureError{cause: err}
}

type attachmentSpineRPCProcess struct {
	cancel context.CancelFunc
	cmd    *exec.Cmd
	input  io.WriteCloser
	output io.ReadCloser
	stderr boundedBuffer

	mu       sync.Mutex
	closed   bool
	waitOnce sync.Once
	waitErr  error
}

func dialPrivateAttachmentSpineRPC() (attachmentRPCChannel, error) {
	return dialPrivateAttachmentSpineRPCProfile("__attachment-publisher-spine-rpc")
}

func dialPrivateAttachmentTerminalSpineRPC() (attachmentRPCChannel, error) {
	return dialPrivateAttachmentSpineRPCProfile("__attachment-terminal-spine-rpc")
}

func dialPrivateAttachmentSpineRPCProfile(command string) (attachmentRPCChannel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helperWallTimeout)
	helperDeadline := time.Now().Add(privateHelperSelfTimeout).UnixMilli()
	cmd := exec.CommandContext(ctx,
		"docker", "exec", "-i", helperContainerName(), "mc",
		command,
		"--deadline-unix-ms", strconv.FormatInt(helperDeadline, 10))
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		cancel()
		return nil, err
	}
	process := &attachmentSpineRPCProcess{
		cancel: cancel, cmd: cmd, input: input, output: output,
	}
	process.stderr.limit = maxHelperStderrBytes
	cmd.Stderr = &process.stderr
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		cancel()
		return nil, fmt.Errorf("private attachment spine RPC failed to start")
	}
	return process, nil
}

func (p *attachmentSpineRPCProcess) Exchange(
	ctx context.Context,
	request attachmentSpineRPCRequest,
) (attachmentSpineRPCResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var response attachmentSpineRPCResponse
	if p.closed {
		return response, errors.New("attachment spine RPC process is closed")
	}
	type result struct {
		response attachmentSpineRPCResponse
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		if err := writePrivateFrame(p.input, request); err != nil {
			completed <- result{err: err}
			return
		}
		var response attachmentSpineRPCResponse
		err := readPrivateMessage(p.output, &response)
		completed <- result{response: response, err: err}
	}()
	timer := time.NewTimer(helperNoProgressTimeout)
	defer timer.Stop()
	select {
	case result := <-completed:
		if result.err != nil {
			p.abort()
			return response, p.processError(result.err)
		}
		return result.response, nil
	case <-ctx.Done():
		p.abort()
		return response, ctx.Err()
	case <-timer.C:
		p.abort()
		return response, fmt.Errorf(
			"private attachment spine RPC made no I/O progress for %s",
			helperNoProgressTimeout)
	}
}

func (p *attachmentSpineRPCProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.wait()
	}
	p.closed = true
	_ = p.input.Close()
	_ = p.output.Close()
	err := p.wait()
	p.cancel()
	return err
}

func (p *attachmentSpineRPCProcess) abort() {
	if p.closed {
		return
	}
	p.closed = true
	_ = p.input.Close()
	_ = p.output.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	p.cancel()
}

func (p *attachmentSpineRPCProcess) wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
	})
	return p.waitErr
}

func (p *attachmentSpineRPCProcess) processError(cause error) error {
	waitErr := p.wait()
	reason := helperReason(&p.stderr, waitErr)
	if reason == "" {
		return fmt.Errorf("private attachment spine RPC failed: %w", cause)
	}
	return fmt.Errorf("private attachment spine RPC failed%s: %w", reason, cause)
}

var (
	_ driver.Driver         = (*attachmentSpineProxyDriver)(nil)
	_ driver.Connector      = (*attachmentSpineProxyConnector)(nil)
	_ driver.Conn           = (*attachmentSpineProxyConn)(nil)
	_ driver.ConnBeginTx    = (*attachmentSpineProxyConn)(nil)
	_ driver.ExecerContext  = (*attachmentSpineProxyConn)(nil)
	_ driver.QueryerContext = (*attachmentSpineProxyConn)(nil)
	_ driver.Tx             = (*attachmentSpineProxyTx)(nil)
	_ driver.Rows           = (*attachmentSpineProxyRows)(nil)
)
