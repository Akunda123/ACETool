import soundfile as sf, numpy as np, glob, os
root=r"F:\新建文件夹 (11)\Index-TTS-V26\Index-TTS-V26\dist\index-tts-windows-cu128-deepspeed\app\prompts\library"
def f0m(x,sr):
    fl=int(0.04*sr);hop=int(0.02*sr);f0=[]
    for i in range(0,len(x)-fl,hop):
        fr=x[i:i+fl]
        if np.sqrt((fr**2).mean())<0.01:continue
        fr=fr-fr.mean();ac=np.correlate(fr,fr,"full")[len(fr)-1:]
        lo,hi=int(sr/400),int(sr/60)
        if hi>=len(ac):continue
        k=lo+int(np.argmax(ac[lo:hi]))
        if k>0 and ac[k]>0.3*ac[0]:f0.append(sr/k)
    return float(np.median(f0)) if f0 else 0.0
for p in sorted(glob.glob(os.path.join(root,"*.wav"))):
    x,sr=sf.read(p,dtype="float32")
    if x.ndim>1:x=x.mean(axis=1)
    print("%-10s %7.2fs %5d Hz  F0~%5.1f  peak %5.1f dBFS"%(os.path.basename(p),len(x)/sr,sr,f0m(x,sr),20*np.log10(max(float(np.abs(x).max()),1e-6))))
