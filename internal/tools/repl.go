package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// REPLTool runs Python in a persistent interpreter that keeps state (variables,
// imports, definitions) across calls within a session. Loosely inspired by
// vibelearn's REPL; here it is a concrete stateful code-eval tool. A small
// driver reads length-framed code blocks, execs them in one namespace, and
// returns output framed by a sentinel so capture is reliable.
type REPLTool struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func (r *REPLTool) Name() string { return "REPL" }

func (r *REPLTool) Description() string {
	return "Execute Python code in a persistent interpreter. State (variables, imports, function/class " +
		"definitions) persists across calls within this session. Returns stdout, the repr of the last " +
		"expression, and any traceback. Pass reset=true to restart with a fresh namespace."
}

func (r *REPLTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"code": {"type": "string", "description": "Python source to execute"},
			"reset": {"type": "boolean", "description": "Restart the interpreter with a fresh namespace"}
		},
		"required": ["code"]
	}`)
}

const replSentinel = "__BANKAI_REPL_DONE__"

// replDriver reads blocks of `<n>\n<n bytes of code>` from stdin, execs each in
// a shared namespace, prints captured stdout + last-expression repr + any
// traceback, then a sentinel line so the caller knows the block finished.
const replDriver = `
import sys, io, ast, traceback
ns = {"__name__": "__main__"}
SENT = "` + replSentinel + `"
def run(src):
    buf = io.StringIO()
    old = sys.stdout
    sys.stdout = buf
    result_repr = None
    try:
        mod = ast.parse(src, mode="exec")
        last_expr = None
        if mod.body and isinstance(mod.body[-1], ast.Expr):
            last_expr = mod.body.pop()
        code = compile(mod, "<repl>", "exec")
        exec(code, ns)
        if last_expr is not None:
            val = eval(compile(ast.Expression(last_expr.value), "<repl>", "eval"), ns)
            if val is not None:
                result_repr = repr(val)
    except Exception:
        sys.stdout = old
        buf.write(traceback.format_exc())
        sys.stdout = buf
    finally:
        sys.stdout = old
    out = buf.getvalue()
    if result_repr is not None:
        if out and not out.endswith("\n"):
            out += "\n"
        out += result_repr
    return out
while True:
    header = sys.stdin.readline()
    if not header:
        break
    try:
        n = int(header.strip())
    except ValueError:
        continue
    src = sys.stdin.read(n)
    sys.stdout.write(run(src))
    sys.stdout.write("\n" + SENT + "\n")
    sys.stdout.flush()
`

func (r *REPLTool) start(ctx context.Context) error {
	cmd := exec.Command("python3", "-u", "-c", replDriver)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // fold stderr in (driver already captures most)
	if err := cmd.Start(); err != nil {
		return err
	}
	r.cmd = cmd
	r.stdin = stdin
	r.stdout = bufio.NewReader(stdout)
	return nil
}

func (r *REPLTool) stop() {
	if r.stdin != nil {
		_ = r.stdin.Close()
	}
	if r.cmd != nil {
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
	}
	r.cmd, r.stdin, r.stdout = nil, nil, nil
}

func (r *REPLTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Code  string `json:"code"`
		Reset bool   `json:"reset"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return Result{IsError: true, Output: "python3 not found on PATH"}, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if in.Reset {
		r.stop()
	}
	if r.cmd == nil {
		if err := r.start(ctx); err != nil {
			return Result{IsError: true, Output: "failed to start python: " + err.Error()}, nil
		}
	}

	// Send the code block: length header + raw source.
	if _, err := fmt.Fprintf(r.stdin, "%d\n%s", len(in.Code), in.Code); err != nil {
		r.stop()
		return Result{IsError: true, Output: "repl write failed: " + err.Error()}, nil
	}

	// Read until the sentinel line.
	var buf bytes.Buffer
	for {
		line, err := r.stdout.ReadString('\n')
		if err != nil {
			r.stop()
			return Result{IsError: true, Output: "repl closed unexpectedly:\n" + buf.String()}, nil
		}
		if strings.TrimRight(line, "\n") == replSentinel {
			break
		}
		buf.WriteString(line)
	}
	out := strings.TrimRight(buf.String(), "\n")
	if out == "" {
		out = "(no output)"
	}
	isErr := strings.Contains(out, "Traceback (most recent call last)")
	return Result{Output: out, IsError: isErr}, nil
}
