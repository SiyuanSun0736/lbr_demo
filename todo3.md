抽象符号解析器接口: 提取 UserSymbolResolver/DwarfResolver 的公共方法为接口。
替换外部 addr2line 调用: 用内建 ELF/DWARF 解析替代 shell 调用以提高可靠性。
重构 SFrame 解析器: 拆分与简化 sframe_parser.go，修复 V3/FLEX 逻辑并补充注释。
统一进程映射加载: 合并 GetProcessMaps/mapping 逻辑，减少重复代码。
封装 /proc 与 ptrace IO: 抽象 proc_io.go 中读写与寄存器获取，便于测试。
增加单元测试与示例: 为关键解析路径添加单元与集成测试（包括示例二进制）。
清理重复代码与日志: 统一 debugLog、错误格式与资源关闭（defer）。
优化批量解析性能: 改善 ResolveBatchAddresses 的偏移计算与并发处理。
