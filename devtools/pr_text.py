import gzip,re
p=r"C:/Users/<user>\Documents\Adobe\Premiere Pro\22.0\acetool.prproj"
x=gzip.decompress(open(p,"rb").read()).decode("utf-8","ignore")
cjk=re.findall(r"[\u4e00-\u9fff][^\x00<]{0,60}", x)
print("CJK chunks:", len(cjk))
for s in cjk[:25]: print(repr(s))
print("---- FormattedTextData sample ----")
i=x.find("FormattedTextData")
print(x[i-300:i+600])
