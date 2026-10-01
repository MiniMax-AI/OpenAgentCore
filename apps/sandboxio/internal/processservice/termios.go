//go:build linux

package processservice

import (
	"os"
	"slices"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

type termField uint8

const (
	fieldCC termField = iota
	fieldIflag
	fieldLflag
	fieldOflag
	fieldCsize
	fieldCflag
)

// termMode maps a supported terminal mode to termios: a control-character
// index, a flag bit, or a character size.
type termMode struct {
	field termField
	value uint32
}

// termModes are the RFC 4254 modes Linux implements. VDSUSP, VFLUSH, VSWTCH
// and VSTATUS have no Linux character, and a PTY has no line speed.
var termModes = map[sp.PTYMode]termMode{
	sp.ModeVINTR:    {fieldCC, unix.VINTR},
	sp.ModeVQUIT:    {fieldCC, unix.VQUIT},
	sp.ModeVERASE:   {fieldCC, unix.VERASE},
	sp.ModeVKILL:    {fieldCC, unix.VKILL},
	sp.ModeVEOF:     {fieldCC, unix.VEOF},
	sp.ModeVEOL:     {fieldCC, unix.VEOL},
	sp.ModeVEOL2:    {fieldCC, unix.VEOL2},
	sp.ModeVSTART:   {fieldCC, unix.VSTART},
	sp.ModeVSTOP:    {fieldCC, unix.VSTOP},
	sp.ModeVSUSP:    {fieldCC, unix.VSUSP},
	sp.ModeVREPRINT: {fieldCC, unix.VREPRINT},
	sp.ModeVWERASE:  {fieldCC, unix.VWERASE},
	sp.ModeVLNEXT:   {fieldCC, unix.VLNEXT},
	sp.ModeVDISCARD: {fieldCC, unix.VDISCARD},
	sp.ModeIGNPAR:   {fieldIflag, unix.IGNPAR},
	sp.ModePARMRK:   {fieldIflag, unix.PARMRK},
	sp.ModeINPCK:    {fieldIflag, unix.INPCK},
	sp.ModeISTRIP:   {fieldIflag, unix.ISTRIP},
	sp.ModeINLCR:    {fieldIflag, unix.INLCR},
	sp.ModeIGNCR:    {fieldIflag, unix.IGNCR},
	sp.ModeICRNL:    {fieldIflag, unix.ICRNL},
	sp.ModeIUCLC:    {fieldIflag, unix.IUCLC},
	sp.ModeIXON:     {fieldIflag, unix.IXON},
	sp.ModeIXANY:    {fieldIflag, unix.IXANY},
	sp.ModeIXOFF:    {fieldIflag, unix.IXOFF},
	sp.ModeIMAXBEL:  {fieldIflag, unix.IMAXBEL},
	sp.ModeIUTF8:    {fieldIflag, unix.IUTF8},
	sp.ModeISIG:     {fieldLflag, unix.ISIG},
	sp.ModeICANON:   {fieldLflag, unix.ICANON},
	sp.ModeXCASE:    {fieldLflag, unix.XCASE},
	sp.ModeECHO:     {fieldLflag, unix.ECHO},
	sp.ModeECHOE:    {fieldLflag, unix.ECHOE},
	sp.ModeECHOK:    {fieldLflag, unix.ECHOK},
	sp.ModeECHONL:   {fieldLflag, unix.ECHONL},
	sp.ModeNOFLSH:   {fieldLflag, unix.NOFLSH},
	sp.ModeTOSTOP:   {fieldLflag, unix.TOSTOP},
	sp.ModeIEXTEN:   {fieldLflag, unix.IEXTEN},
	sp.ModeECHOCTL:  {fieldLflag, unix.ECHOCTL},
	sp.ModeECHOKE:   {fieldLflag, unix.ECHOKE},
	sp.ModePENDIN:   {fieldLflag, unix.PENDIN},
	sp.ModeOPOST:    {fieldOflag, unix.OPOST},
	sp.ModeOLCUC:    {fieldOflag, unix.OLCUC},
	sp.ModeONLCR:    {fieldOflag, unix.ONLCR},
	sp.ModeOCRNL:    {fieldOflag, unix.OCRNL},
	sp.ModeONOCR:    {fieldOflag, unix.ONOCR},
	sp.ModeONLRET:   {fieldOflag, unix.ONLRET},
	sp.ModeCS7:      {fieldCsize, unix.CS7},
	sp.ModeCS8:      {fieldCsize, unix.CS8},
	sp.ModePARENB:   {fieldCflag, unix.PARENB},
	sp.ModePARODD:   {fieldCflag, unix.PARODD},
}

func supportedModes() []sp.PTYMode {
	modes := make([]sp.PTYMode, 0, len(termModes))
	for m := range termModes {
		modes = append(modes, m)
	}
	slices.Sort(modes)
	return modes
}

// applyModes sets the requested modes on the terminal. A character value of
// 255 disables the character. CS7 or CS8 set to 1 selects that character
// size; set to 0 it leaves the size unchanged.
func applyModes(tty *os.File, modes []sp.PTYModeValue) error {
	fd := int(tty.Fd())
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	for _, mv := range modes {
		m := termModes[mv.Mode]
		switch m.field {
		case fieldCC:
			c := uint8(mv.Value)
			if mv.Value == sp.DisabledChar {
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
	return unix.IoctlSetTermios(fd, unix.TCSETS, t)
}

func setFlag(flags, bit, value uint32) uint32 {
	if value == 1 {
		return flags | bit
	}
	return flags &^ bit
}

func winsize(s sp.WindowSize) *unix.Winsize {
	return &unix.Winsize{Row: s.Rows, Col: s.Cols, Xpixel: s.XPixels, Ypixel: s.YPixels}
}
