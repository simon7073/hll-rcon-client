package core

// xorEncrypt 循环异或加密/解密
//
// XOR 是对称操作：加密和解密使用同一个函数。
// key 为空时明文透传（用于 ServerConnect 阶段）。
func xorEncrypt(data, key []byte) []byte {
	if len(key) == 0 {
		out := make([]byte, len(data))
		copy(out, data)
		return out
	}
	result := make([]byte, len(data))
	for i := range data {
		result[i] = data[i] ^ key[i%len(key)]
	}
	return result
}
