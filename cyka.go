package main

import (
    "os"
    "fmt"
    "log"
    "time"
    "bytes"
    "unsafe"
    "strconv"

    "net/http"
    "io/ioutil"
    "encoding/binary"

    "github.com/fatih/color"
    "github.com/valyala/fastjson"
    "github.com/gorilla/websocket"
)

const (
    protocolVersion = 1
    defaultWsUrl = "wss://broadcastlv.chat.bilibili.com/sub"
    // defaultWsUrl = "wss://tx-live-dmcmt-sv-01.chat.bilibili.com/sub"
    // defaultWsUrl = "wss://tx-live-dmcmt-tor-01.chat.bilibili.com/sub"
    initUrl = "https://api.live.bilibili.com/room/v1/Room/room_init?id="
)

const (
    opSendHeartbeat = 2
    opPopularity    = 3
    opCommand       = 5
    opAuth          = 7
    opRecvHeartbeat = 8
)

type header struct {
    Size uint32
    HeaderSize uint16
    ProtocolVersion uint16
    Operation uint32
    Sequence uint32
}

const headerSize = unsafe.Sizeof(header{})

type packet struct {
    header
    data []byte
}

func (p *packet) bytes() ([]byte, error) {
    buf := &bytes.Buffer{}

    err := binary.Write(buf, binary.BigEndian, &p.header)
    if err != nil {
        return nil, err
    }

    err = binary.Write(buf, binary.BigEndian, &p.data)
    if err != nil {
        return nil, err
    }

    return buf.Bytes(), nil
}

func newPacketFromBytes(b []byte) (*packet, error) {
    p := packet{}

    buf := bytes.NewBuffer(b)

    err := binary.Read(buf, binary.BigEndian, &p.header)
    if err != nil {
        return nil, err
    }

    p.data = make([]byte, p.Size - uint32(headerSize))

    err = binary.Read(buf, binary.BigEndian, &p.data)
    if err != nil {
        return nil, err
    }

    return &p, nil
}

func newPacket(op int, d []byte) *packet {
    return &packet{
        header{
            uint32(headerSize + uintptr(len(d))),
            uint16(headerSize),
            protocolVersion,
            uint32(op),
            1,
        },
        d,
    }
}

var (
    logger = log.New(os.Stderr, "", log.LstdFlags)
    dinger = log.New(os.Stdout, "🎃 ", 0)
)

var (
    green = color.New(color.Bold, color.BgGreen).SprintfFunc()
    blue = color.New(color.Bold, color.BgBlue).SprintfFunc()
    magenta = color.New(color.FgMagenta).SprintfFunc()
    cyan = color.New(color.Bold, color.FgCyan).SprintfFunc()
    yellow = color.New(color.FgYellow).SprintfFunc()
    red = color.New(color.FgRed).SprintfFunc()
)

const usage = `Usage:
	%s ROOM [SERVER default="%s"]
`

var room int

func main() {
    args := os.Args[1:]

    if len(args) < 1 {
        fmt.Printf(usage, os.Args[0], defaultWsUrl)
        os.Exit(1)
    }

    room, err := strconv.Atoi(args[0])

    if room < 0 {
        fmt.Printf(usage, os.Args[0], defaultWsUrl)
        os.Exit(1)
    }

    var url = defaultWsUrl

    if len(args) > 1 {
        url = args[1]
    }

    logger.Println(magenta("获取房间..."))
    resp, err := http.Get(initUrl+args[0])
    if err != nil {
        logger.Fatal("get failed")
    }
    defer resp.Body.Close()
    body, err := ioutil.ReadAll(resp.Body)
    room = fastjson.GetInt(body, "data", "room_id")
    logger.Println(magenta("获取成功"))

    logger.Println(magenta("服务器 %s", url))
    logger.Println(magenta("房间 %d", room))

    logger.Println(magenta("连接服务器..."))
    conn, _, err := websocket.DefaultDialer.Dial(url, nil)
    if err != nil {
        logger.Fatal("dial failed")
    }
    defer conn.Close()
    logger.Println(magenta("连接成功"))

    logger.Println(magenta("验证信息..."))
    err = sendAuth(conn, room)
    if err != nil {
        logger.Fatal("auth failed")
    }
    logger.Println(magenta("验证成功"))

    t := time.NewTicker(30 * time.Second)
    go func() {
        for {
            select {
            case <-t.C:
                sendHeartbeat(conn)
            }
        }
    }()

    for {
        _, msg, err := conn.ReadMessage()
        if err != nil {
            logger.Panic(err)
        }

        p, err := newPacketFromBytes(msg)
        if err != nil {
            logger.Panic(err)
        }

        switch p.Operation {
        case opPopularity:
            logger.Println(blue("人气 %d", binary.BigEndian.Uint32(p.data)))
        case opCommand:
            go handleCommand(p.data)
        case opRecvHeartbeat:
            go sendHeartbeat(conn)
        default: logger.Println("unknown op:", p.Operation)
        }
    }
}

func handleCommand(cmd []byte) {
    switch command := fastjson.GetString(cmd, "cmd"); command {
        case "DANMU_MSG":
            user := fastjson.GetString(cmd, "info", "2", "1")
            text := fastjson.GetString(cmd, "info", "1")
            dinger.Printf("%s: %s\n", cyan(user), text)
        case "SEND_GIFT":
            user := fastjson.GetString(cmd, "data", "uname")
            action := fastjson.GetString(cmd, "data", "action")
            gift := fastjson.GetString(cmd, "data", "giftName")
            num := fastjson.GetInt(cmd, "data", "num")
            dinger.Printf("%s %s %s x%d\n", cyan(user), yellow(action), red(gift), num)
        case "WELCOME":
        case "WELCOME_GUARD":
        case "SYS_MSG":
        case "PREPARING":
        case "LIVE":
        case "WISH_BOTTLE":
        case "NOTICE_MSG":
        case "ROOM_RANK":
        case "COMBO_SEND":
        case "COMBO_END":
        case "ROOM_BLOCK_MSG":
        case "ENTRY_EFFECT":
        default:
            logger.Println("unknown command:", command, string(cmd))
    }
}

func send(conn *websocket.Conn, op int, msg string) error {
    p := newPacket(op, []byte(msg))

    b, err := p.bytes()
    if err != nil {
        return err
    }

    err = conn.WriteMessage(websocket.BinaryMessage, b)
    if err != nil {
        return err
    }

    return nil
}

const (
    emptyJSON = `{}`
    authJSONTemplate = `{"uid":0,"roomid":%d,"protover":1,"platform":"web","clientver":"1.4.0"}`
)

func sendHeartbeat(conn *websocket.Conn) error {
    logger.Println(green("心跳"))
    return send(conn, opSendHeartbeat, emptyJSON)
}

func sendAuth(conn *websocket.Conn, id int) error {
    return send(conn, opAuth, fmt.Sprintf(authJSONTemplate, id))
}
