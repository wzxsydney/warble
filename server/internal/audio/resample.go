package audio

// Resample converts int16 PCM from srcHz to dstHz using linear
// interpolation. Quality is acceptable for speech; the device has a
// 1 W speaker so we're not trying to preserve audiophile fidelity.
//
// Tested ratios: 22050 → 24000 (Piper output → device), 24000 → 16000
// (downstream PCM tests).
func Resample(pcm []int16, srcHz, dstHz int) []int16 {
	if srcHz == dstHz || len(pcm) == 0 {
		out := make([]int16, len(pcm))
		copy(out, pcm)
		return out
	}
	ratio := float64(srcHz) / float64(dstHz)
	outLen := int(float64(len(pcm)) / ratio)
	out := make([]int16, outLen)
	for i := 0; i < outLen; i++ {
		srcF := float64(i) * ratio
		srcIdx := int(srcF)
		frac := srcF - float64(srcIdx)
		if srcIdx >= len(pcm)-1 {
			out[i] = pcm[len(pcm)-1]
			continue
		}
		a := float64(pcm[srcIdx])
		b := float64(pcm[srcIdx+1])
		v := a + (b-a)*frac
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}
