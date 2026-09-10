package codec

// Native μ-law ↔ PCM16 conversion — zero ffmpeg subprocesses

// ULawToPCM16 decodes a single μ-law byte to 16-bit PCM sample
func ULawToPCM16(ulaw byte) int16 {
	ulaw = ^ulaw
	sign := int16(1)
	if ulaw&0x80 != 0 {
		sign = -1
		ulaw &= 0x7F
	}
	exponent := int((ulaw >> 4) & 0x07)
	mantissa := int(ulaw & 0x0F)
	sample := int16((mantissa<<(uint(exponent)+1) | (1 << uint(exponent))) + (1 << uint(exponent)) - 33)
	return sign * sample
}

// ULawToPCM16Bytes converts a slice of μ-law bytes to interleaved little-endian PCM16
func ULawToPCM16Bytes(ulaw []byte) []byte {
	out := make([]byte, len(ulaw)*2)
	for i, b := range ulaw {
		s := ULawToPCM16(b)
		out[i*2] = byte(s)
		out[i*2+1] = byte(s >> 8)
	}
	return out
}

// PCM16ToULaw encodes a single 16-bit PCM sample to μ-law
func PCM16ToULaw(sample int16) byte {
	const bias = 33
	const clip = 32635

	sign := byte(0)
	if sample < 0 {
		sign = 0x80
		sample = -sample
	}
	if sample > clip {
		sample = clip
	}
	sample += bias

	exponent := 7
	expMask := int16(0x4000)
	for i := 7; i >= 0; i-- {
		if sample&expMask != 0 {
			exponent = i
			break
		}
		expMask >>= 1
	}

	mantissa := int((int(sample)>>(uint(exponent)+1))&0x0F) & 0x0F
	ulawByte := ^(sign | byte(exponent<<4) | byte(mantissa))
	return ulawByte
}

// PCM16BytesToULaw converts interleaved PCM16 bytes to μ-law
func PCM16BytesToULaw(pcm []byte) []byte {
	out := make([]byte, len(pcm)/2)
	for i := 0; i < len(pcm)-1; i += 2 {
		sample := int16(pcm[i]) | int16(pcm[i+1])<<8
		out[i/2] = PCM16ToULaw(sample)
	}
	return out
}
