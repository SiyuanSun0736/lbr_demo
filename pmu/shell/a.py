import matplotlib.font_manager as fm

# 列出所有已注册字体中包含 'CJK' 或 'Noto' 的名称
fonts = [f.name for f in fm.fontManager.ttflist if 'CJK' in f.name or 'Noto' in f.name]
print(set(fonts))