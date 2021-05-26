package commands

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
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

var (
	Debug = log.New(os.Stdout, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	Info  = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	Error = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
)

type tesmartSwitch struct {
	conn net.Conn

	err       chan error
	responses chan []byte
}

func NewTesmartSwitch(host string) (*tesmartSwitch, error) {
	t := tesmartSwitch{}

	err := t.connect(host)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

func (t *tesmartSwitch) SwitchInput(input int) (int, error) {
	if input < 1 && input > 16 {
		return 0, errors.New("invalid input value")
	}

	command := injectInputToPayload(SWITCH_INPUT, byte(input))
	t.send(command)
	return extractInput(<-t.responses)
}

func (t *tesmartSwitch) SetLedTimeout(input int) (int, error) {
	if input < 0 && input > 30 {
		return 0, errors.New("invalid LED timeout value")
	}

	command := injectInputToPayload(GET_CURRENT_INPUT, byte(input))
	t.send(command)
	return extractInput(<-t.responses)
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

func (t *tesmartSwitch) GetCurrentInput() (int, error) {
	t.send(GET_CURRENT_INPUT)
	return extractInput(<-t.responses)
}

// do it properly
func (t *tesmartSwitch) connect(host string) error {
	Debug.Print("Connecting...")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		Error.Printf("Failed to dial: %v", err)
		return err
	}

	t.conn = conn
	Debug.Print("Connected")

	t.responses = make(chan []byte, 2)
	t.err = make(chan error, 1)

	go func() {
		for {
			fmt.Println("loopino")
			err := <-t.err
			fmt.Println("err")
			if io.EOF == err {
				fmt.Println("EOF")
				return
			}
			t.connect(host)
			return

		}
	}()

	go t.receiveLoop()

	return nil
}

func (t *tesmartSwitch) send(command []byte) error {
	Debug.Printf("Sending: %#v (%s)", command, hex.EncodeToString(command))

	bytesSent, err := t.conn.Write(command)
	if err != nil {
		Error.Printf("Failed to send command: %v", err)
		return err
	}

	if bytesSent != 6 {
		err := fmt.Errorf("wrong amount of byte sent: %d. Expected 6", bytesSent)
		Error.Printf(err.Error())
		return err
	}
	Debug.Printf("Sent: %d", bytesSent)

	return nil
}

// DO IT PROPERLY
func (t *tesmartSwitch) receiveLoop() {
	log.Print("Receive loop start")
	for {
		log.Print("reading")
		response := make([]byte, 6)

		err := t.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err != nil {
			log.Println("SetReadDeadline failed:", err)
			// do something else, for example create new conn
			return
		}

		read, err := t.conn.Read(response)

		log.Printf("read %d bytes: %s", read, hex.EncodeToString(response))
		if err != nil {
			log.Printf("Failed to fetch: %v", err)
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Println("read timeout:", err)
				// return
			}

			// if io.EOF == err {
			// 	return
			// }
			t.err <- err
			break
		}

		t.responses <- response
	}
}

func injectInputToPayload(payload []byte, input byte) []byte {
	command := make([]byte, 6)
	copy(command, payload)
	command[4] = input
	return command
}

func extractInput(response []byte) (int, error) {
	if isValidOutput(response) {
		return int(response[4]) + 1, nil // input is zero based
	}
	return 0, errors.New("invalid response")
}

func isValidOutput(output []byte) bool {
	Debug.Printf("isValidOutput = %s", hex.EncodeToString(output))

	return output[0] == 0xAA &&
		output[1] == 0xBB &&
		output[2] == 0x03 &&
		output[3] == 0x11 &&
		output[5]-output[4] == 0x16 // checksum?
}
