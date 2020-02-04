// I should go first

// +build js,wasm

package mqtt

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall/js"
	"time"
)

type JSWSAddr struct {
	schema string
	path   string
}

func (w JSWSAddr) Network() string {
	return w.schema
}

func (w JSWSAddr) String() string {
	return w.path
}

var localAddr = JSWSAddr{schema: "ws", path: "localhost"}
var arrayBuffer = js.Global().Get("ArrayBuffer")
var uint8Array = js.Global().Get("Uint8Array")
var blob = js.Global().Get("Blob")
var fileReader = js.Global().Get("FileReader")

func NewWebsocket(host string, c *tls.Config, timeout time.Duration, requestHeader http.Header) (net.Conn, error) {
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
	}()

	if strings.HasPrefix(host, "wss://") {
		return nil, fmt.Errorf("secured websocket were currently not supported")
	}

	ws := js.Global().Get("WebSocket").New(host)
	wg.Add(1)
	ws.Call("addEventListener", "open", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		fmt.Println("web socket is open")
		wg.Done()
		return nil
	}))

	ret := &JSWebsocket{ws: ws, path: host}
	ret.Init()
	return ret, nil
}

type JSWebsocket struct {
	ws   js.Value
	buf  *blockingBuffer
	path string
}

func (w *JSWebsocket) Init() {
	w.buf = newBlockingBuffer()
	w.ws.Call("addEventListener", "message", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		evt := args[0]
		datav := evt.Get("data")
		var buf []byte
		syncRecv := true
		if datav.Type() == js.TypeString {
			log.Println("It is an String")
			buf = []byte(datav.String())
		} else if datav.InstanceOf(arrayBuffer) {
			log.Println("It is an ArrayBuffer!")
			arr := uint8Array.New(datav)
			buf = make([]byte, arr.Get("byteLength").Int())
			js.CopyBytesToGo(buf, arr)
		} else if datav.InstanceOf(blob) {
			log.Println("It is an Blob!")
			syncRecv = false
			fr := fileReader.New()
			fr.Call("addEventListener", "loadend", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				abuf := fr.Get("result")
				arr := uint8Array.New(abuf)
				buf = make([]byte, arr.Get("byteLength").Int())
				js.CopyBytesToGo(buf, arr)
				log.Printf("Blob loaded!")
				log.Printf("message received, len = %v, buf = %s\n", len(buf), buf)
				w.buf.Write(buf)
				return nil
			}))
			go fr.Call("readAsArrayBuffer", datav)
		} else {
			fmt.Println("It is not an ArrayBuffer!")
			return nil
		}

		if syncRecv {
			w.buf.Write(buf)
		}
		return nil
	}))
	return
}

func (w *JSWebsocket) Read(b []byte) (n int, err error) {
	log.Printf("Reading %d bytes...\n", len(b))

	return w.buf.Read(b)
}

func (w *JSWebsocket) Write(b []byte) (n int, err error) {
	arr := uint8Array.New(len(b))
	js.CopyBytesToJS(arr, b)
	w.ws.Call("send", arr.Get("buffer"))
	log.Printf("message [%s] sent!", b)
	return len(b), nil
}

func (w *JSWebsocket) Close() error {
	w.ws.Call("close")
	return nil
}

func (w *JSWebsocket) LocalAddr() net.Addr {
	return localAddr
}

func (w *JSWebsocket) RemoteAddr() net.Addr {
	return JSWSAddr{schema: "ws", path: w.path}
}

func (w *JSWebsocket) SetDeadline(t time.Time) error {
	return nil
}

func (w *JSWebsocket) SetReadDeadline(t time.Time) error {
	return nil
}

func (w *JSWebsocket) SetWriteDeadline(t time.Time) error {
	return nil
}

type blockingBuffer struct {
	buf  bytes.Buffer
	cond *sync.Cond
}

func newBlockingBuffer() *blockingBuffer {
	m := sync.Mutex{}
	return &blockingBuffer{
		cond: sync.NewCond(&m),
		buf:  bytes.Buffer{},
	}
}

func (br *blockingBuffer) Write(b []byte) (ln int, err error) {
	ln, err = br.buf.Write(b)
	br.cond.Broadcast()
	return
}

func (br *blockingBuffer) Read(b []byte) (ln int, err error) {
	ln, err = br.buf.Read(b)
	if err == io.EOF {
		br.cond.L.Lock()
		br.cond.Wait()
		br.cond.L.Unlock()
		ln, err = br.buf.Read(b)
	}
	return
}
