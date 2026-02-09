package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
)

const maxControlLineBytes = 4 * 1024 * 1024

type ControlModeSession struct {
	session  string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	onOutput func(paneID string, data []byte)
	stopOnce sync.Once
	mu       sync.Mutex
}

func NewControlModeSession(sessionID string, onOutput func(paneID string, data []byte)) *ControlModeSession {
	return &ControlModeSession{
		session:  sessionID,
		onOutput: onOutput,
	}
}

func (c *ControlModeSession) Start() error {
	c.cmd = exec.Command("tmux", "-C", "attach-session", "-t", c.session)
	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		return err
	}
	c.stdout, err = c.cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := c.cmd.Start(); err != nil {
		return err
	}

	go c.parseOutput()
	return nil
}

func (c *ControlModeSession) Stop() {
	c.stopOnce.Do(func() {
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}

func (c *ControlModeSession) parseOutput() {
	reader := bufio.NewReaderSize(c.stdout, 1024*1024)
	inBlock := false

	for {
		line, err := readControlLine(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("control-mode read error: %v", err)
			return
		}

		if len(line) == 0 {
			continue
		}

		switch {
		case bytes.HasPrefix(line, []byte("%output ")):
			if inBlock {
				continue
			}
			rest := line[len("%output "):]
			idx := bytes.IndexByte(rest, ' ')
			if idx <= 0 {
				continue
			}
			paneID := string(rest[:idx])
			data := unescapeControlData(rest[idx+1:])
			if c.onOutput != nil {
				c.onOutput(paneID, data)
			}

		case bytes.HasPrefix(line, []byte("%begin ")):
			inBlock = true

		case bytes.HasPrefix(line, []byte("%end ")):
			inBlock = false

		case bytes.HasPrefix(line, []byte("%error ")):
			inBlock = false

		case bytes.HasPrefix(line, []byte("%exit")):
			return
		default:
			if inBlock {
				continue
			}
		}
	}
}

func readControlLine(reader *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		part, err := reader.ReadBytes('\n')
		buf = append(buf, part...)
		if len(buf) > maxControlLineBytes {
			return nil, fmt.Errorf("control line exceeds %d bytes", maxControlLineBytes)
		}
		if err == nil {
			return bytes.TrimRight(buf, "\r\n"), nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(buf) > 0 {
			return bytes.TrimRight(buf, "\r\n"), io.EOF
		}
		return nil, err
	}
}

func unescapeControlData(input []byte) []byte {
	if bytes.IndexByte(input, '\\') == -1 {
		return input
	}
	output := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		b := input[i]
		if b != '\\' || i+1 >= len(input) {
			output = append(output, b)
			continue
		}
		i++
		switch input[i] {
		case 'n':
			output = append(output, '\n')
		case 'r':
			output = append(output, '\r')
		case 't':
			output = append(output, '\t')
		case 'e':
			output = append(output, 0x1b)
		case '\\':
			output = append(output, '\\')
		case 'x':
			if i+2 < len(input) {
				value := hexValue(input[i+1])<<4 | hexValue(input[i+2])
				if value >= 0 {
					output = append(output, byte(value))
					i += 2
					break
				}
			}
			output = append(output, '\\', input[i])
		case '0', '1', '2', '3', '4', '5', '6', '7':
			value := int(input[i] - '0')
			for j := 0; j < 2 && i+1 < len(input); j++ {
				next := input[i+1]
				if next < '0' || next > '7' {
					break
				}
				value = value*8 + int(next-'0')
				i++
			}
			output = append(output, byte(value))
		default:
			output = append(output, '\\', input[i])
		}
	}
	return output
}

func hexValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}

func (c *ControlModeSession) SendKeys(paneID string, keys []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return
	}
	fmt.Fprintf(c.stdin, "send-keys -l -t %s %q\n", paneID, string(keys))
}

func (c *ControlModeSession) Resize(paneID string, cols, rows int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return
	}
	fmt.Fprintf(c.stdin, "resize-pane -t %s -x %d -y %d\n", paneID, cols, rows)
}

func (c *ControlModeSession) RefreshClient(cols, rows int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return
	}
	fmt.Fprintf(c.stdin, "refresh-client -C %d,%d\n", cols, rows)
}
