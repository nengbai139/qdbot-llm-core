// Package protocol 定义客户端与 tunnel-server 之间的隧道帧协议。
//
// 帧格式：type(1) + id(8) + len(4) + payload
//
//	type=0 REQUEST  服务端 → 客户端，转发的 HTTP 请求
//	type=1 RESPONSE 客户端 → 服务端，HTTP 响应
//	type=2 ASSIGNED 服务端 → 客户端，分配到的公网 URL
//	type=3 PING     客户端 → 服务端，心跳（探测链路是否还活着）
//	type=4 PONG     服务端 → 客户端，心跳应答
//
// 两端各写一份帧编解码迟早会对不上，所以放在同一个包里。
package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// TypeRequest 服务端转发给客户端的 HTTP 请求。
	TypeRequest byte = 0
	// TypeResponse 客户端回给服务端的 HTTP 响应。
	TypeResponse byte = 1
	// TypeAssigned 服务端告知客户端分到的公网 URL。
	TypeAssigned byte = 2
	// TypePing 客户端心跳。
	TypePing byte = 3
	// TypePong 服务端心跳应答。
	TypePong byte = 4
)

const (
	// HeaderSize 帧头长度：type(1)+id(8)+len(4)。
	HeaderSize = 13
	// MaxPayload 单帧最大负载，防止恶意长度把内存吃光。
	MaxPayload = 10 * 1024 * 1024
)

// Encode 组装一帧。
func Encode(typ byte, id uint64, payload []byte) []byte {
	frame := make([]byte, HeaderSize+len(payload))
	frame[0] = typ
	binary.BigEndian.PutUint64(frame[1:9], id)
	binary.BigEndian.PutUint32(frame[9:13], uint32(len(payload)))
	copy(frame[HeaderSize:], payload)
	return frame
}

// ReadFrame 读取一帧。负载超限或读不完整都返回错误，不返回半截数据。
func ReadFrame(r io.Reader) (typ byte, id uint64, payload []byte, err error) {
	head := make([]byte, HeaderSize)
	if _, err = io.ReadFull(r, head); err != nil {
		return 0, 0, nil, err
	}
	typ = head[0]
	id = binary.BigEndian.Uint64(head[1:9])
	length := binary.BigEndian.Uint32(head[9:13])
	if length > MaxPayload {
		return 0, 0, nil, fmt.Errorf("帧负载超限: %d", length)
	}
	if length == 0 {
		return typ, id, nil, nil
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, 0, nil, err
	}
	return typ, id, payload, nil
}
