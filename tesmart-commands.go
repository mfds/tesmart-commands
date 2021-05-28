package commands

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
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
	Debug = log.New(os.Stdout, "DEBUG: ", 0)
	Info  = log.New(os.Stdout, "INFO: ", 0)
	Error = log.New(os.Stderr, "ERROR: ", 0)
)

type tesmartSwitch struct {
	conn net.Conn

	err       chan error
	responses chan []byte
	Responses chan []byte
	sw        chan bool
}

func NewTesmartSwitch(host string) (*tesmartSwitch, error) {
	t := tesmartSwitch{}

	hasDebugFlag := flag.Bool("v", false, "enable debug messages")
	flag.Parse()

	if hasDebugFlag == nil || !(*hasDebugFlag) {
		Debug.SetOutput(ioutil.Discard)
	}

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

func (t *tesmartSwitch) connect(host string) error {
	Debug.Print("Connecting...")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		Error.Printf("Failed to dial: %v", err)
		return err
	}

	t.conn = conn
	Info.Printf("Connected to: %s", host)

	t.responses = make(chan []byte, 1)
	t.Responses = make(chan []byte, 1)
	t.err = make(chan error, 1)
	t.sw = make(chan bool, 1)

	go t.receiveLoop()

	go func() {
		<-t.err
		Debug.Println("Error detected, reconnecting...")
		t.conn.Close()
		go t.connect(host)
	}()

	return nil
}

func (t *tesmartSwitch) send(command []byte) error {
	Debug.Printf("Sending: %#v (%s)", command, hex.EncodeToString(command))

	bytesSent, err := t.conn.Write(command)
	if err != nil {
		Error.Printf("Failed to send command: %v", err)
		return err
	}
	t.sw <- true

	if bytesSent != 6 {
		err := fmt.Errorf("wrong amount of byte sent: %d. Expected 6", bytesSent)
		Error.Printf(err.Error())
		return err
	}
	Debug.Printf("Sent: %d", bytesSent)

	return nil
}

func (t *tesmartSwitch) receiveLoop() {
	Debug.Print("Receive loop start")
	for {
		Debug.Print("Reading message")
		response := make([]byte, 6)

		read, err := t.conn.Read(response)
		Debug.Printf("Read %d bytes: %s", read, hex.Dump(response))
		if err != nil {
			Debug.Printf("Failed to read data from socket: %v", err)

			t.err <- err
			break
		}

		select {
		case <-t.sw:
			t.responses <- response
		default:
			t.Responses <- response
		}
	}
	Debug.Print("Receive loop end")
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
