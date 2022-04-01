package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var (
	SWITCH_INPUT                 = []byte{0xAA, 0xBB, 0x03, 0x01, 0x00, 0xEE} // 5th byte is input (1-16 or 1-8)
	SET_LED_TIMEOUT              = []byte{0xAA, 0xBB, 0x03, 0x03, 0x00, 0xEE} // 5th byte is input (in secs. 0x00 to 0x1E. 0 will disable timeout)
	MUTE_BUZZER                  = []byte{0xAA, 0xBB, 0x03, 0x02, 0x00, 0xEE}
	UNMUTE_BUZZER                = []byte{0xAA, 0xBB, 0x03, 0x02, 0x01, 0xEE}
	ENABLE_AUTO_INPUT_DETECTION  = []byte{0xAA, 0xBB, 0x03, 0x81, 0x01, 0xEE} // Only on the 8 port model
	DISABLE_AUTO_INPUT_DETECTION = []byte{0xAA, 0xBB, 0x03, 0x81, 0x00, 0xEE} // Only on the 8 port model
	GET_CURRENT_INPUT            = []byte{0xAA, 0xBB, 0x03, 0x10, 0x00, 0xEE}

	OUTPUT = []byte{0xAA, 0xBB, 0x03, 0x11} // last two bytes are: input and input+0x16 (checksum probably)
)

var Debug = log.New(ioutil.Discard, "DEBUG: ", 0)

type tesmartSwitch struct {
	host          string
	conn          net.Conn
	connectionCtx context.Context
	cancelFunc    context.CancelFunc
	receiverFunc  func([]byte)
}

func NewTesmartSwitch(host string, port string, receiverFunc func([]byte)) (*tesmartSwitch, error) {
	t := tesmartSwitch{}

	if _, ok := os.LookupEnv("DEBUG"); ok {
		Debug.SetOutput(os.Stdout)
	}

	err := t.connect(host, port)
	if err != nil {
		return nil, err
	}

	t.receiverFunc = receiverFunc

	return &t, nil
}

func (t *tesmartSwitch) SwitchInput(input int) error {
	if input < 1 && input > 16 {
		return errors.New("invalid input value")
	}

	command := injectInputToPayload(SWITCH_INPUT, byte(input))
	return t.send(command)
}

func (t *tesmartSwitch) SetLedTimeout(input int) error {
	if input < 0 && input > 30 {
		return errors.New("invalid LED timeout value")
	}

	command := injectInputToPayload(GET_CURRENT_INPUT, byte(input))
	return t.send(command)
}

func (t *tesmartSwitch) MuteBuzzer() error {
	return t.send(MUTE_BUZZER)
}

func (t *tesmartSwitch) UnmuteBuzzer() error {
	return t.send(UNMUTE_BUZZER)
}

func (t *tesmartSwitch) EnableAutoInputDetection() error {
	return t.send(ENABLE_AUTO_INPUT_DETECTION)
}

func (t *tesmartSwitch) DisableAutoInputDetection() error {
	return t.send(DISABLE_AUTO_INPUT_DETECTION)
}

func (t *tesmartSwitch) SendGetCurrentInput() error {
	return t.send(GET_CURRENT_INPUT)
}

func (t *tesmartSwitch) connect(host string, port string) error {
	Debug.Print("Connecting...")
	var d net.Dialer

	dialCtx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := d.DialContext(dialCtx, "tcp", host+":"+port)
	if err != nil {
		Debug.Printf("Failed to dial: %v", err)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())

	t.host = host
	t.conn = conn
	t.connectionCtx = ctx
	t.cancelFunc = cancel

	go t.receiveLoop()
	go t.checkConnectionLoop()

	Debug.Printf("Connected to: %s", host+":"+port)

	return nil
}

func (t *tesmartSwitch) send(command []byte) error {
	Debug.Printf("Sending: %s", printHex(command))

	bytesSent, err := t.conn.Write(command)
	if err != nil {
		Debug.Printf("Failed to send command: %v", err)
		return err
	}

	if bytesSent != 6 {
		err := fmt.Errorf("wrong amount of byte sent: %d. Expected 6", bytesSent)
		Debug.Printf(err.Error())
		return err
	}
	Debug.Printf("Sent: %d", bytesSent)

	return nil
}

func (t *tesmartSwitch) checkConnectionLoop() {
	defer func() {
		t.cancelFunc()
		t.conn.Close()
	}()

	for {
		cmd := exec.Command("ping", "-c4", t.host)

		if err := cmd.Start(); err != nil {
			log.Fatalf("cmd.Start: %v", err)
		}

		if err := cmd.Wait(); err != nil {
			if exiterr, ok := err.(*exec.ExitError); ok {
				if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
					if status.ExitStatus() != 0 {
						Debug.Printf("Disconnected")
						return
					}
				}
			} else {
				log.Fatalf("cmd.Wait: %v", err)
			}
		}
		Debug.Println("PING")
	}
}

func (t *tesmartSwitch) receiveLoop() {
	defer t.conn.Close()

ReadLoop:
	for {
		select {
		case <-t.connectionCtx.Done():
			return
		default:
			response := make([]byte, 6)
			t.conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
			read, err := t.conn.Read(response)

			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
					continue ReadLoop
				} else if err != io.EOF {
					Debug.Printf("Failed to read data from socket: %v", err)
					return
				}
			}

			if read == 0 {
				return
			}

			Debug.Printf("Read %d bytes: %s", read, printHex(response))

			t.receiverFunc(response)
		}
	}
}

func injectInputToPayload(payload []byte, input byte) []byte {
	command := make([]byte, 6)
	copy(command, payload)
	command[4] = input
	return command
}

func ExtractInput(response []byte) (int, error) {
	if isValidOutput(response) {
		return int(response[4]) + 1, nil // input is zero based
	}
	return 0, errors.New("invalid response")
}

func isValidOutput(output []byte) bool {
	Debug.Printf("isValidOutput = %s", printHex(output))

	return len(output) == 6 &&
		output[0] == 0xAA &&
		output[1] == 0xBB &&
		output[2] == 0x03 &&
		output[3] == 0x11 &&
		output[5]-output[4] == 0x16 // checksum?
}

func printHex(data []byte) (out string) {
	out = "\033[1D"
	for _, b := range data {
		out += strings.ToUpper(fmt.Sprintf("%3.2x", b))
	}
	return
}
