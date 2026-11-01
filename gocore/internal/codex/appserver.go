package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("codex app-server: %d: %s", e.Code, e.Message) }

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type appServer struct {
	stdout         io.ReadCloser
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	scanner        *bufio.Scanner
	cancel         context.CancelFunc
	timeout, grace time.Duration
}

func (p *Provider) probe(ctx context.Context, binary string) (data json.RawMessage, err error) {
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(processCtx, binary, "app-server")
	configureProcess(cmd)
	// Env remains nil: inherit the environment unchanged, including CODEX_HOME.
	cmd.Stderr = io.Discard
	cmd.WaitDelay = p.shutdownGrace
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, unavailable("codex app-server: cannot open stdin")
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, unavailable("codex app-server: cannot open stdout")
	}
	defer func() { _ = stdout.Close() }()
	if err = cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, timeoutError()
		}
		return nil, unavailable("codex app-server: cannot start binary: " + err.Error())
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	server := appServer{stdout, cmd, stdin, scanner, cancel, p.callTimeout, p.shutdownGrace}
	defer func() {
		waitErr := server.stop(err != nil)
		var providerErr *usage.Error
		if err != nil && !errors.As(err, &providerErr) {
			err = unavailable(fmt.Sprintf("codex app-server: stdout closed before response (exit code %d)", cmd.ProcessState.ExitCode()))
		}
		// A complete answer is kept even when the process then exits non-zero:
		// the data was already read and validated, its exit status adds nothing.
		_ = waitErr
		if err != nil && ctx.Err() != nil {
			err = timeoutError()
		}
	}()
	_, err = server.call(ctx, 1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "aipermiler", "title": nil, "version": buildinfo.Version}}, false)
	if err != nil {
		return nil, classifyRPC(err)
	}
	data, err = server.call(ctx, 2, "account/rateLimits/read", nil, true)
	var rpc *rpcError
	if errors.As(err, &rpc) && rpc.Code == -32600 {
		var accountData json.RawMessage
		accountData, err = server.call(ctx, 3, "account/read", map[string]bool{"refreshToken": false}, false)
		if err != nil {
			return nil, classifyRPC(err)
		}
		var account struct {
			Account *struct {
				Type string `json:"type"`
			} `json:"account"`
		}
		if json.Unmarshal(accountData, &account) == nil && account.Account != nil && account.Account.Type == "apiKey" {
			return nil, unavailable("Codex is signed in with an API key, which has no subscription quota")
		}
		return nil, &usage.Error{Code: usage.CodeSignedOut, Message: "Sign in with Codex (CLI or extension)"}
	}
	return data, classifyRPC(err)
}

func classifyRPC(err error) error {
	var rpc *rpcError
	if errors.As(err, &rpc) {
		if rpc.Code == -32603 {
			return unavailable("codex app-server: " + rpc.Message)
		}
		return unavailable(rpc.Error())
	}
	return err
}

func timeoutError() *usage.Error {
	return &usage.Error{Code: usage.CodeTimeout, Message: "codex app-server: request timed out"}
}

func (s *appServer) call(ctx context.Context, id int, method string, params any, initialized bool) (json.RawMessage, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	type reply struct {
		data json.RawMessage
		err  error
	}
	done := make(chan reply, 1)
	go func() {
		encoder := json.NewEncoder(s.stdin)
		if initialized {
			if err := encoder.Encode(struct {
				Method string `json:"method"`
			}{"initialized"}); err != nil {
				done <- reply{nil, err}
				return
			}
		}
		request := struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params any    `json:"params"`
		}{id, method, params}
		if err := encoder.Encode(request); err != nil {
			done <- reply{nil, err}
			return
		}
		data, err := s.readResponse(id)
		done <- reply{data, err}
	}()
	select {
	case result := <-done:
		return result.data, result.err
	case <-callCtx.Done():
		s.cancel()
		// Kill closes the peer's pipes, unblocking both writes and reads. Join before returning.
		_ = s.cmd.Process.Kill()
		_ = s.stdout.Close()
		_ = s.stdin.Close()
		<-done
		return nil, timeoutError()
	}
}

func (s *appServer) readResponse(id int) (json.RawMessage, error) {
	for s.scanner.Scan() {
		var response rpcResponse
		if json.Unmarshal(s.scanner.Bytes(), &response) != nil {
			continue
		}
		var responseID int
		if json.Unmarshal(response.ID, &responseID) != nil || responseID != id {
			continue
		}
		if response.Error != nil {
			return nil, response.Error
		}
		// Missing or non-object results are drift, without exposing parser errors.
		if len(response.Result) == 0 {
			return nil, drift(nil)
		}
		var decoded any
		if json.Unmarshal(response.Result, &decoded) != nil {
			return nil, drift(nil)
		}
		if _, ok := decoded.(map[string]any); !ok {
			return nil, drift(decoded)
		}
		return response.Result, nil
	}
	if s.scanner.Err() != nil {
		return nil, unavailable("codex app-server: cannot read response")
	}
	return nil, io.EOF
}

func (s *appServer) stop(failed bool) error {
	if failed {
		s.cancel()
		_ = s.cmd.Process.Kill()
	}
	_ = s.stdin.Close()
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = s.cmd.Wait(); close(done) }()
	timer := time.NewTimer(s.grace)
	defer timer.Stop()
	select {
	case <-done:
		return waitErr
	case <-timer.C:
		s.cancel()
		_ = s.cmd.Process.Kill()
		<-done
		return nil
	}
}
