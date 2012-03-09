/*
    Package keybind provides an easy interface to bind and run callback
    functions for human readable keybindings.
*/
package keybind

import "fmt"
import "log"
import "strings"

import "code.google.com/p/jamslam-x-go-binding/xgb"
import "github.com/BurntSushi/xgbutil"
import "github.com/BurntSushi/xgbutil/xevent"

var modifiers []uint16 = []uint16{ // order matters!
    xgb.ModMaskShift, xgb.ModMaskLock, xgb.ModMaskControl,
    xgb.ModMask1, xgb.ModMask2, xgb.ModMask3, xgb.ModMask4, xgb.ModMask5,
    xgb.ModMaskAny,
}

// Initialize attaches the appropriate callbacks to make key bindings easier.
// i.e., update state of the world on a MappingNotify.
func Initialize(xu *xgbutil.XUtil) {
    // Listen to mapping notify events
    xevent.MappingNotifyFun(updateMaps).Connect(xu, xgbutil.NoWindow)

    // Give us an initial mapping state...
    keyMap, modMap := mapsGet(xu)
    xu.KeyMapSet(keyMap)
    xu.ModMapSet(modMap)
}

func updateMaps(xu *xgbutil.XUtil, e xevent.MappingNotifyEvent) {
    keyMap, modMap := mapsGet(xu)
    xu.KeyMapSet(keyMap)
    xu.ModMapSet(modMap)
}

func minMaxKeycodeGet(xu *xgbutil.XUtil) (byte, byte) {
    return xu.Conn().Setup.MinKeycode, xu.Conn().Setup.MaxKeycode
}

func mapsGet(xu *xgbutil.XUtil) (*xgb.GetKeyboardMappingReply,
                                 *xgb.GetModifierMappingReply) {
    min, max := minMaxKeycodeGet(xu)
    newKeymap, keyErr := xu.Conn().GetKeyboardMapping(min, max - min + 1)
    newModmap, modErr := xu.Conn().GetModifierMapping()

    // If there are errors, we really need to panic. We just can't do
    // any key binding without a mapping from the server.
    if keyErr != nil {
        panic(fmt.Sprintf("COULD NOT GET KEYBOARD MAPPING: %v\n" +
                          "THIS IS AN UNRECOVERABLE ERROR.\n",
                          keyErr))
    }
    if modErr != nil {
        panic(fmt.Sprintf("COULD NOT GET MODIFIER MAPPING: %v\n" +
                          "THIS IS AN UNRECOVERABLE ERROR.\n",
                          keyErr))
    }

    return newKeymap, newModmap
}

// ParseString takes a string of the format '[Mod[-Mod[...]]-]-KEY',
// i.e., 'Mod4-j', and returns a modifiers/keycode combo.
// (Actually, the parser is slightly more forgiving than what this comment
//  leads you to believe.)
func ParseString(xu *xgbutil.XUtil, str string) (uint16, byte) {
    mods, kc := uint16(0), byte(0)
    for _, part := range strings.Split(str, "-") {
        switch(strings.ToLower(part)) {
        case "shift":
            mods |= xgb.ModMaskShift
        case "lock":
            mods |= xgb.ModMaskLock
        case "control":
            mods |= xgb.ModMaskControl
        case "mod1":
            mods |= xgb.ModMask1
        case "mod2":
            mods |= xgb.ModMask2
        case "mod3":
            mods |= xgb.ModMask3
        case "mod4":
            mods |= xgb.ModMask4
        case "mod5":
            mods |= xgb.ModMask5
        case "any":
            mods |= xgb.ModMaskAny
        default: // a key code!
            if kc == 0 { // only accept the first keycode we see
                kc = lookupString(xu, part)
            }
        }
    }

    if kc == 0 {
        log.Printf("We could not find a valid keycode in the string '%s'. " +
                   "Things probably will not work right.\n", str)
    }

    return mods, kc
}

// lookupString is a wrapper around keycodeGet meant to make our search
// a bit more flexible if needed. (i.e., case-insensitive)
func lookupString(xu *xgbutil.XUtil, str string) byte {
    // Do some fancy case stuff before we give up.
    sym, ok := keysyms[str]
    if !ok {
        sym, ok = keysyms[strings.Title(str)]
    }
    if !ok {
        sym, ok = keysyms[strings.ToLower(str)]
    }
    if !ok {
        sym, ok = keysyms[strings.ToUpper(str)]
    }

    // If we don't know what 'str' is, return 0.
    // There will probably be a bad access. We should do better than that...
    if !ok {
        return byte(0)
    }

    return keycodeGet(xu, sym)
}

// Given a keysym, find the keycode mapped to it in the current X environment.
// keybind.Initialize MUST have been called before using this function.
func keycodeGet(xu *xgbutil.XUtil, keysym xgb.Keysym) byte {
    min, max := minMaxKeycodeGet(xu)
    keyMap := xu.KeyMapGet()

    var c byte
    for kc := int(min); kc <= int(max); kc++ {
        for c = 0; c < keyMap.KeysymsPerKeycode; c++ {
            if keysym == keysymGet(xu, byte(kc), c) {
                return byte(kc)
            }
        }
    }
    return 0
}

// Given a keycode and a column, find the keysym associated with it in
// the current X environment.
// keybind.Initialize MUST have been called before using this function.
func keysymGet(xu *xgbutil.XUtil, keycode byte, column byte) xgb.Keysym {
    min, _ := minMaxKeycodeGet(xu)
    keyMap := xu.KeyMapGet()
    i := (int(keycode) - int(min)) * int(keyMap.KeysymsPerKeycode) + int(column)

    return keyMap.Keysyms[i]
}

// modGet finds the modifier currently associated with a given keycode.
// If a modifier doesn't exist for this keycode, then 0 is returned.
func modGet(xu *xgbutil.XUtil, keycode byte) uint16 {
    modMap := xu.ModMapGet()

    var i byte
    for i = 0; int(i) < len(modMap.Keycodes); i++ {
        if modMap.Keycodes[i] == keycode {
            return modifiers[i / modMap.KeycodesPerModifier]
        }
    }

    return 0
}

// XModMap should replicate the output of 'xmodmap'.
// This is mainly a sanity check, and may serve as an example of how to
// use modifier mappings.
func XModMap(xu *xgbutil.XUtil) {
    fmt.Println("Replicating `xmodmap`...")
    modMap := xu.ModMapGet()
    kPerMod := int(modMap.KeycodesPerModifier)

    // some nice names for the modifiers like xmodmap
    nice := []string{
        "shift", "lock", "control", "mod1", "mod2", "mod3", "mod4", "mod5",
    }

    var row int
    var comma string
    for mmi, _ := range modifiers[:len(modifiers) - 1] { // skip 'ModMaskAny'
        row = mmi * kPerMod
        comma = ""

        fmt.Printf("%s\t\t", nice[mmi])
        for _, kc := range modMap.Keycodes[row:row + kPerMod] {
            if kc != 0 {
                // This trickery is where things get really complicated.
                // We throw our hands up in the air if the first two columns
                // in our key map give us nothing.
                // But how do we know which column is the right one? I'm not
                // sure. This is what makes going from key code -> english
                // so difficult. The answer is probably buried somewhere
                // in the implementation of XLookupString in xlib. *shiver*
                ksym := keysymGet(xu, kc, 0)
                if ksym == 0 {
                    ksym = keysymGet(xu, kc, 1)
                }
                fmt.Printf("%s %s (0x%X)", comma, strKeysyms[ksym], kc)
                comma = ","
            }
        }
        fmt.Printf("\n")
    }
}

// Grabs a key with mods on a particular window.
// Will also grab all combinations of modifiers found in xgbutil.IgnoreMods
func Grab(xu *xgbutil.XUtil, win xgb.Id, mods uint16, key byte) {
    for _, m := range xgbutil.IgnoreMods {
        xu.Conn().GrabKey(true, win, mods | m, key,
                          xgb.GrabModeAsync, xgb.GrabModeAsync)
    }
}
