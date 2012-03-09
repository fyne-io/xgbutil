package main

import (
    "fmt"
    // "os" 
)

import (
    "code.google.com/p/jamslam-x-go-binding/xgb"

    "github.com/BurntSushi/xgbutil"
    // "github.com/BurntSushi/xgbutil/ewmh" 
    "github.com/BurntSushi/xgbutil/keybind"
    "github.com/BurntSushi/xgbutil/xevent"
    "github.com/BurntSushi/xgbutil/xprop"
    "github.com/BurntSushi/xgbutil/xwindow"
)

func MyCallback(X *xgbutil.XUtil, e xevent.PropertyNotifyEvent) {
    atomName, err := xprop.AtomName(X, e.Atom)
    if err != nil {
        panic(err)
    } else {
        fmt.Printf("property %s, state %v\n", atomName, e.State)
    }
}

func MyCallback2(X *xgbutil.XUtil, e xevent.MappingNotifyEvent) {
    fmt.Printf("MappingNotify | Request = %v, FirstKeycode = %v, Count = %v\n",
               e.Request, e.FirstKeycode, e.Count)
}

func KeyPressCallback(X *xgbutil.XUtil, e xevent.KeyPressEvent) {
    fmt.Printf("Key press callback!\n")
}

func KeyReleaseCallback(X *xgbutil.XUtil, e xevent.KeyReleaseEvent) {
    fmt.Printf("Key release callback!\n")
}

func main() {
    fmt.Printf("Starting...\n")
    X, _ := xgbutil.Dial("")

    // active, _ := ewmh.ActiveWindowGet(X) 

    xwindow.Listen(X, X.RootWin(), xgb.EventMaskPropertyChange)

    cb := xevent.PropertyNotifyFun(MyCallback)
    cb.Connect(X, X.RootWin())

    keybind.Initialize(X)

    keycbPress := keybind.KeyPressFun(KeyPressCallback)
    keycbPress.Connect(X, X.RootWin(), "XF86Sleep") // Mod4-j

    keybind.XModMap(X)

    // keycbRelease := keybind.KeyReleaseFun(KeyReleaseCallback) 
    // keycbRelease.Connect(X, X.RootWin(), "Mod4-j") // Mod4-j 

    // fmt.Println(keybind.ParseString(X, "F1")) 

    xevent.Main(X)
}
