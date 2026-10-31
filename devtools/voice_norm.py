import wave, struct, math, sys, os

def resample(vals, sr, target=24000):
    if sr == target: return vals, sr
    n = int(round(len(vals)*target/sr)); out=[]
    for i in range(n):
        x=i*sr/target; i0=int(x); f=x-i0
        a=vals[min(i0,len(vals)-1)]; b=vals[min(i0+1,len(vals)-1)]
        out.append(int(a+(b-a)*f))
    return out, target

def proc(src, dst, max_sec=30.0, peak_db=-6.0):
    w=wave.open(src,'rb'); sr,ch,sw,n=w.getframerate(),w.getnchannels(),w.getsampwidth(),w.getnframes()
    d=w.readframes(n); w.close()
    if sw!=2: raise SystemExit('only 16bit')
    vals=list(struct.unpack('<%dh'%(len(d)//2),d))
    if ch==2: vals=[(vals[i]+vals[i+1])//2 for i in range(0,len(vals)-1,2)]
    th=int(32768*10**(-45/20)); i=0
    while i<len(vals) and abs(vals[i])<th: i+=1
    j=len(vals)-1
    while j>i and abs(vals[j])<th: j-=1
    i=max(0,i-sr//30); j=min(len(vals)-1,j+sr//30); vals=vals[i:j+1]
    if len(vals)>max_sec*sr: vals=vals[:int(max_sec*sr)]
    vals,sr=resample(vals,sr)
    peak=max(1,max(abs(v) for v in vals)); g=10**(peak_db/20)*32768/peak
    vals=[max(-32768,min(32767,int(v*g))) for v in vals]
    f=int(sr*0.02)
    for k in range(min(f,len(vals))): vals[k]=int(vals[k]*k/f); vals[-1-k]=int(vals[-1-k]*k/f)
    o=wave.open(dst,'wb'); o.setnchannels(1); o.setsampwidth(2); o.setframerate(sr)
    o.writeframes(struct.pack('<%dh'%len(vals),*vals)); o.close()
    print('%s  %.2fs  %d Hz  peak %.1f dBFS'%(dst,len(vals)/sr,sr,20*math.log10(max(abs(v) for v in vals)/32768)))

if __name__=='__main__':
    for name in sys.argv[1:]:
        src='docs/voice/%s.wav'%name
        if os.path.exists(src): proc(src, 'docs/voice/%s_24k.wav'%name)
        else: print('missing', src)
