package protocol

import (
	"bytes"
	"testing"
)

// TestEncodeDecodeRoundTrip 帧编解码必须能原样还原，否则请求体会被截断。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		typ     byte
		id      uint64
		payload []byte
	}{
		{"请求", TypeRequest, 42, []byte("GET / HTTP/1.1\r\n\r\n")},
		{"响应", TypeResponse, 42, []byte("HTTP/1.1 200 OK\r\n\r\nbody")},
		{"分配结果", TypeAssigned, 0, []byte("https://x.tunnel.example.com")},
		{"心跳", TypePing, 0, nil},
		{"心跳应答", TypePong, 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frame := Encode(c.typ, c.id, c.payload)
			if len(frame) != HeaderSize+len(c.payload) {
				t.Fatalf("帧长不对：%d", len(frame))
			}
			typ, id, payload, err := ReadFrame(bytes.NewReader(frame))
			if err != nil {
				t.Fatalf("读帧失败: %v", err)
			}
			if typ != c.typ || id != c.id {
				t.Fatalf("头部解错：type=%d id=%d", typ, id)
			}
			if !bytes.Equal(payload, c.payload) {
				t.Fatalf("负载解错：%q", payload)
			}
		})
	}
}

// TestReadFrameRejectsOversizedPayload 长度字段是对方给的，必须校验，
// 否则一个坏长度就能让服务端申请几百 MB 内存。
func TestReadFrameRejectsOversizedPayload(t *testing.T) {
	frame := Encode(TypeRequest, 1, nil)
	// 把长度改成超限值
	frame[9] = 0xFF
	frame[10] = 0xFF
	frame[11] = 0xFF
	frame[12] = 0xFF
	if _, _, _, err := ReadFrame(bytes.NewReader(frame)); err == nil {
		t.Fatal("超限负载应报错")
	}
}

// TestReadFrameTruncated 半截帧必须报错，不能返回空负载冒充成功。
func TestReadFrameTruncated(t *testing.T) {
	frame := Encode(TypeRequest, 1, []byte("hello"))
	if _, _, _, err := ReadFrame(bytes.NewReader(frame[:HeaderSize+2])); err == nil {
		t.Fatal("截断帧应报错")
	}
	if _, _, _, err := ReadFrame(bytes.NewReader(nil)); err == nil {
		t.Fatal("空输入应报错")
	}
}
