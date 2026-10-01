//go:build linux

package sandboxprocess

import (
	"slices"

	"golang.org/x/sys/unix"
)

// This file is the Linux termios projection of the terminal modes, used both
// to read a local terminal into a PTYSpec and to apply a PTYSpec to a new one.

type termField uint8

const (
	fieldCC termField = iota
	fieldIflag
	fieldLflag
	fieldOflag
	fieldCsize
	fieldCflag
)

// termMode maps a mode to termios: a control-character index, a flag bit, or
// a character size.
type termMode struct {
	field termField
	value uint32
}

// termModes are the modes Linux implements. VDSUSP, VFLUSH, VSWTCH and VSTATUS
// have no Linux character, and a PTY has no line speed.
var termModes = map[PTYMode]termMode{
	ModeVINTR:    {fieldCC, unix.VINTR},
	ModeVQUIT:    {fieldCC, unix.VQUIT},
	ModeVERASE:   {fieldCC, unix.VERASE},
	ModeVKILL:    {fieldCC, unix.VKILL},
	ModeVEOF:     {fieldCC, unix.VEOF},
	ModeVEOL:     {fieldCC, unix.VEOL},
	ModeVEOL2:    {fieldCC, unix.VEOL2},
	ModeVSTART:   {fieldCC, unix.VSTART},
	ModeVSTOP:    {fieldCC, unix.VSTOP},
	ModeVSUSP:    {fieldCC, unix.VSUSP},
	ModeVREPRINT: {fieldCC, unix.VREPRINT},
	ModeVWERASE:  {fieldCC, unix.VWERASE},
	ModeVLNEXT:   {fieldCC, unix.VLNEXT},
	ModeVDISCARD: {fieldCC, unix.VDISCARD},
	ModeIGNPAR:   {fieldIflag, unix.IGNPAR},
	ModePARMRK:   {fieldIflag, unix.PARMRK},
	ModeINPCK:    {fieldIflag, unix.INPCK},
	ModeISTRIP:   {fieldIflag, unix.ISTRIP},
	ModeINLCR:    {fieldIflag, unix.INLCR},
	ModeIGNCR:    {fieldIflag, unix.IGNCR},
	ModeICRNL:    {fieldIflag, unix.ICRNL},
	ModeIUCLC:    {fieldIflag, unix.IUCLC},
	ModeIXON:     {fieldIflag, unix.IXON},
	ModeIXANY:    {fieldIflag, unix.IXANY},
	ModeIXOFF:    {fieldIflag, unix.IXOFF},
	ModeIMAXBEL:  {fieldIflag, unix.IMAXBEL},
	ModeIUTF8:    {fieldIflag, unix.IUTF8},
	ModeISIG:     {fieldLflag, unix.ISIG},
	ModeICANON:   {fieldLflag, unix.ICANON},
	ModeXCASE:    {fieldLflag, unix.XCASE},
	ModeECHO:     {fieldLflag, unix.ECHO},
	ModeECHOE:    {fieldLflag, unix.ECHOE},
	ModeECHOK:    {fieldLflag, unix.ECHOK},
	ModeECHONL:   {fieldLflag, unix.ECHONL},
	ModeNOFLSH:   {fieldLflag, unix.NOFLSH},
	ModeTOSTOP:   {fieldLflag, unix.TOSTOP},
	ModeIEXTEN:   {fieldLflag, unix.IEXTEN},
	ModeECHOCTL:  {fieldLflag, unix.ECHOCTL},
	ModeECHOKE:   {fieldLflag, unix.ECHOKE},
	ModePENDIN:   {fieldLflag, unix.PENDIN},
	ModeOPOST:    {fieldOflag, unix.OPOST},
	ModeOLCUC:    {fieldOflag, unix.OLCUC},
	ModeONLCR:    {fieldOflag, unix.ONLCR},
	ModeOCRNL:    {fieldOflag, unix.OCRNL},
	ModeONOCR:    {fieldOflag, unix.ONOCR},
	ModeONLRET:   {fieldOflag, unix.ONLRET},
	ModeCS7:      {fieldCsize, unix.CS7},
	ModeCS8:      {fieldCsize, unix.CS8},
	ModePARENB:   {fieldCflag, unix.PARENB},
	ModePARODD:   {fieldCflag, unix.PARODD},
}

// LinuxModes returns the modes Linux termios implements, sorted.
func LinuxModes() []PTYMode {
	modes := make([]PTYMode, 0, len(termModes))
	for m := range termModes {
		modes = append(modes, m)
	}
	slices.Sort(modes)
	return modes
}

// ReadModes returns the value in t of each mode in modes that Linux
// implements. A disabled control character reads as DisabledChar.
func ReadModes(t *unix.Termios, modes []PTYMode) []PTYModeValue {
	var out []PTYModeValue
	for _, mode := range modes {
		m, ok := termModes[mode]
		if !ok {
			continue
		}
		var v uint32
		switch m.field {
		case fieldCC:
			v = uint32(t.Cc[m.value])
			if v == 0 { // _POSIX_VDISABLE
				v = DisabledChar
			}
		case fieldIflag:
			v = flagValue(t.Iflag, m.value)
		case fieldLflag:
			v = flagValue(t.Lflag, m.value)
		case fieldOflag:
			v = flagValue(t.Oflag, m.value)
		case fieldCflag:
			v = flagValue(t.Cflag, m.value)
		case fieldCsize:
			if t.Cflag&unix.CSIZE == m.value {
				v = 1
			}
		}
		out = append(out, PTYModeValue{Mode: mode, Value: v})
	}
	return out
}

// ApplyModes sets modes on t, skipping modes Linux does not implement. A
// character value of DisabledChar disables the character. CS7 or CS8 set to 1
// selects that character size; set to 0 it leaves the size unchanged.
func ApplyModes(t *unix.Termios, modes []PTYModeValue) {
	for _, mv := range modes {
		m, ok := termModes[mv.Mode]
		if !ok {
			continue
		}
		switch m.field {
		case fieldCC:
			c := uint8(mv.Value)
			if mv.Value == DisabledChar {
				c = 0 // _POSIX_VDISABLE
			}
			t.Cc[m.value] = c
		case fieldIflag:
			t.Iflag = setFlag(t.Iflag, m.value, mv.Value)
		case fieldLflag:
			t.Lflag = setFlag(t.Lflag, m.value, mv.Value)
		case fieldOflag:
			t.Oflag = setFlag(t.Oflag, m.value, mv.Value)
		case fieldCflag:
			t.Cflag = setFlag(t.Cflag, m.value, mv.Value)
		case fieldCsize:
			if mv.Value == 1 {
				t.Cflag = t.Cflag&^unix.CSIZE | m.value
			}
		}
	}
}

func flagValue(flags, bit uint32) uint32 {
	if flags&bit != 0 {
		return 1
	}
	return 0
}

func setFlag(flags, bit, value uint32) uint32 {
	if value == 1 {
		return flags | bit
	}
	return flags &^ bit
}
