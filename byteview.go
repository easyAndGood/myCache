package mycache

import "bytes"

// data 将会存储真实的缓存值。选择 byte 类型是为了能够支持任意的数据类型的存储，
// 例如字符串、图片等。
type ByteView struct {
	data []byte
}

func (v ByteView) Len() int64 {
	return int64(len(v.data))
}

func (v ByteView) IsEqual(v2 ByteView) bool {
	return bytes.Equal(v.data, v2.data)
}

func (v ByteView) ByteSlice() []byte {
	return cloneBytes(v.data)
}

func (v ByteView) String() string {
	return string(v.data)
}

func cloneBytes(data []byte) []byte {
	c := make([]byte, len(data))
	copy(c, data)
	return c
}
