package lbr

// TODO: 1.PC_MASK部分解析有问题
// TODO: 2.PTL的解析有问题
// TODO: 3.Flex类型的FDE解析未实现

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
)

// parseFREInfo 解析 FRE Info Word
// 位布局（SFrame V3 规范 §2.4.1，From MSB to LSB）：
//
//	bit  0   : fre_cfa_base_reg_id (0=FP, 1=SP)
//	bits 1-4 : fre_dataword_count  SFRAME_FRE_INFO_NUM_OFFSETS_SHIFT=1, MASK=0x1E
//	bits 5-6 : fre_dataword_size   SFRAME_FRE_INFO_DATAWORD_SIZE_SHIFT=5, MASK=0x60
//	bit  7   : fre_mangled_ra_p
//
// 注意：V2 旧布局为 bits 1-2=size、bits 3-6=count，V3 将两者互换。
func parseFREInfo(freInfo uint8) (cfaBaseReg uint8, offsetSize uint8, offsetCount uint8, mangledRA bool) {
	// bit 0: CFA base register (0=FP, 1=SP)
	cfaBaseReg = freInfo & 0x01
	// bits 1-4: fre_dataword_count（最多15个数据字）
	offsetCount = (freInfo >> 1) & 0x0F
	// bits 5-6: fre_dataword_size (0=1B, 1=2B, 2=4B)
	offsetSize = (freInfo >> 5) & 0x03
	// bit 7: mangled RA flag
	mangledRA = (freInfo>>7)&0x01 != 0
	return
}

// parseSFrameDataFromELF 从 ELF 文件中解析 .sframe 节并返回 SFrameData。
// loadSFrameData 和 loadLibraryMapping 均通过此函数复用解析逻辑。
func parseSFrameDataFromELF(elfFile *elf.File) (*SFrameData, error) {
	section := elfFile.Section(".sframe")
	if section == nil {
		return nil, fmt.Errorf("no .sframe section found")
	}

	data, err := section.Data()
	if err != nil {
		return nil, fmt.Errorf("failed to read .sframe section: %w", err)
	}

	if len(data) < 28 {
		return nil, fmt.Errorf("invalid .sframe section size: need at least 28 bytes, got %d", len(data))
	}

	sframe := &SFrameData{
		hasData:     true,
		sectionAddr: section.Addr,
		sectionData: data,
	}

	// 解析 SFrame Preamble (4字节)
	sframe.Header.Magic = binary.LittleEndian.Uint16(data[0:2])
	sframe.Header.Version = data[2]
	sframe.Header.Flags = data[3]

	// 解析 SFrame Header (从偏移4开始)
	sframe.Header.ABI = data[4]
	sframe.Header.FixedFPOffset = int8(data[5])
	sframe.Header.FixedRAOffset = int8(data[6])
	sframe.Header.AuxHdrLen = data[7]
	sframe.Header.NumFDEs = binary.LittleEndian.Uint32(data[8:12])
	sframe.Header.NumFREs = binary.LittleEndian.Uint32(data[12:16])
	sframe.Header.FRELen = binary.LittleEndian.Uint32(data[16:20])
	sframe.Header.FDEOff = binary.LittleEndian.Uint32(data[20:24])
	sframe.Header.FREOff = binary.LittleEndian.Uint32(data[24:28])

	if sframe.Header.Magic != SFrameMagic {
		return nil, fmt.Errorf("invalid SFrame magic: got 0x%x, expected 0x%x",
			sframe.Header.Magic, SFrameMagic)
	}
	debugLog("sframe.Header.Magic : 0x%x", sframe.Header.Magic)
	debugLog("sframe.Header.Version : %d", sframe.Header.Version)
	debugLog("sframe.Header.Flags : 0x%x", sframe.Header.Flags)
	debugLog("sframe.Header.ABI : %d (%s)", sframe.Header.ABI, GetABIDescription(sframe.Header.ABI))
	debugLog("sframe.Header.FixedFPOffset : %d", sframe.Header.FixedFPOffset)
	debugLog("sframe.Header.FixedRAOffset : %d", sframe.Header.FixedRAOffset)
	debugLog("sframe.Header.AuxHdrLen : %d", sframe.Header.AuxHdrLen)
	debugLog("sframe.Header.NumFDEs : %d", sframe.Header.NumFDEs)
	debugLog("sframe.Header.NumFREs : %d", sframe.Header.NumFREs)
	debugLog("sframe.Header.FRELen : %d", sframe.Header.FRELen)
	debugLog("sframe.Header.FDEOff : %d", sframe.Header.FDEOff)
	debugLog("sframe.Header.FREOff : %d", sframe.Header.FREOff)

	// 解析函数条目 (Function Descriptor Entries)
	// V2格式: 20字节/FDE (int32 StartAddr + uint32 Size + uint32 StartFREOff + uint32 NumFREs + uint8 FuncInfo + uint8 RepSize + uint16 Padding)
	// V3格式: 16字节/FDE (int64 StartAddr + uint32 Size + uint32 StartFREOff; 无NumFREs/FuncInfo字段)
	headerSize := 28 + int(sframe.Header.AuxHdrLen)
	fdeStartOffset := headerSize + int(sframe.Header.FDEOff)
	functionEntrySize := 20
	if sframe.Header.Version >= 3 {
		functionEntrySize = 16
	}

	if fdeStartOffset >= len(data) {
		return nil, fmt.Errorf("FDE offset %d exceeds data length %d", fdeStartOffset, len(data))
	}

	offset := fdeStartOffset
	for i := uint32(0); i < sframe.Header.NumFDEs && offset+functionEntrySize <= len(data); i++ {
		var fn SFrameFunction
		if sframe.Header.Version >= 3 {
			fn = SFrameFunction{
				StartAddr:   int64(binary.LittleEndian.Uint64(data[offset : offset+8])),
				Size:        binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
				StartFREOff: binary.LittleEndian.Uint32(data[offset+12 : offset+16]),
				// NumFREs/FuncInfo/RepSize 在 V3 FDE Index 中不存在；稍后由 sframeComputeFREByteLens 计算 FREByteLen
			}
			// 打印解析后的 VA（PCREL: fdeFieldAddr + StartAddr；非PCREL: sectionAddr + StartAddr）
			{
				const SFRAME_F_FDE_FUNC_START_PCREL_INNER = 0x4
				var displayVA uint64
				if sframe.Header.Flags&SFRAME_F_FDE_FUNC_START_PCREL_INNER != 0 {
					fdeFieldAddr := sframe.sectionAddr + uint64(fdeStartOffset+int(i)*functionEntrySize)
					displayVA = uint64(int64(fdeFieldAddr) + fn.StartAddr)
				} else {
					displayVA = sframe.sectionAddr + uint64(fn.StartAddr)
				}
				debugLog("StartAddr=0x%x(pcrel_raw=0x%x), Size=%d, StartFREOff=%d\n",
					displayVA, uint64(fn.StartAddr), fn.Size, fn.StartFREOff)
			}
		} else {
			fn = SFrameFunction{
				StartAddr:   int64(int32(binary.LittleEndian.Uint32(data[offset : offset+4]))),
				Size:        binary.LittleEndian.Uint32(data[offset+4 : offset+8]),
				StartFREOff: binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
				NumFREs:     binary.LittleEndian.Uint32(data[offset+12 : offset+16]),
				FuncInfo:    data[offset+16],
				RepSize:     data[offset+17],
				Padding:     binary.LittleEndian.Uint16(data[offset+18 : offset+20]),
			}
		}
		sframe.Functions = append(sframe.Functions, fn)
		offset += functionEntrySize
	}

	// V3: FDE Index 无 NumFREs/FuncInfo 字段，通过相邻 StartFREOff 差值计算总字节范围
	if sframe.Header.Version >= 3 {
		sframeComputeFREByteLens(sframe.Functions, sframe.Header.FRELen)

		// V3: 解析 sframe_func_desc_attr (5字节) —— 位于每个函数FRE数据的开头
		// 布局: uint16 sfda_func_num_fres | uint8 sfda_func_info | uint8 sfda_func_info2 | uint8 sfda_func_rep_size
		freSubStart := headerSize + int(sframe.Header.FREOff)
		for i := range sframe.Functions {
			fn := &sframe.Functions[i]
			attrOff := freSubStart + int(fn.StartFREOff)
			if attrOff+5 > len(data) {
				debugLog("[DEBUG] parseSFrameDataFromELF: V3 FDE[%d] 属性超出范围: attrOff=%d, dataLen=%d\n",
					i, attrOff, len(data))
				continue
			}
			fn.NumFREs = uint32(binary.LittleEndian.Uint16(data[attrOff : attrOff+2]))
			fn.FuncInfo = data[attrOff+2]
			fn.FuncInfo2 = data[attrOff+3]
			fn.RepSize = data[attrOff+4]
			debugLog("[DEBUG] parseSFrameDataFromELF: V3 FDE[%d] 属性: NumFREs=%d, FuncInfo=0x%x(fre_type=%d,pctype=%d,signal=%v), FuncInfo2=0x%x(fde_type=%d), RepSize=%d\n",
				i, fn.NumFREs,
				fn.FuncInfo, fn.FuncInfo&0x0F, (fn.FuncInfo>>4)&0x01, (fn.FuncInfo>>7) != 0,
				fn.FuncInfo2, fn.FuncInfo2&0x1F,
				fn.RepSize)
		}
	}

	// 对比验证：用逐条解析法（calcFRESize）重新计算 FREByteLen，与差值法结果比较
	if sframe.Header.Version >= 3 {
		parsedLens := sframeComputeFREByteLensByParsing(sframe.Functions, sframe, data)
		mismatchCount := 0
		for i, fn := range sframe.Functions {
			parsed := parsedLens[i]
			diff := fn.FREByteLen
			if parsed != diff {
				mismatchCount++
			}
		}
		if mismatchCount > 0 {
			debugLog("[DEBUG] parseSFrameDataFromELF: FREByteLen 差值法 vs 解析法发现 %d 处不一致（共 %d 个FDE）\n",
				mismatchCount, len(sframe.Functions))
		}
	}

	debugLog("[DEBUG] parseSFrameDataFromELF: 成功解析 %d 个SFrame FDE, Version=%d\n",
		len(sframe.Functions), sframe.Header.Version)
	debugLog("[DEBUG] parseSFrameDataFromELF: FRE数据位于偏移 %d, 总长度 %d\n",
		headerSize+int(sframe.Header.FREOff), sframe.Header.FRELen)

	return sframe, nil
}

// sframeComputeFREByteLens 为 SFrame V3 函数列表计算每个函数的 FRE 字节范围。
// V3 FDE 无 NumFREs 字段，通过比较所有函数的 StartFREOff 确定各函数 FRE 数据的字节边界。
func sframeComputeFREByteLens(functions []SFrameFunction, totalFRELen uint32) {
	for i := range functions {
		nextOff := totalFRELen
		for j := range functions {
			if functions[j].StartFREOff > functions[i].StartFREOff && functions[j].StartFREOff < nextOff {
				nextOff = functions[j].StartFREOff
			}
		}
		functions[i].FREByteLen = nextOff - functions[i].StartFREOff
	}
}

// calcFRESize 计算单条 FRE 的字节大小（不解析偏移值语义，仅计算字节数）。
// 布局：[start_addr: addrSize] [fre_info: 1B] [offsets: offsetCount * offsetBytes]
// addrSize 由 freType 决定（0→1B, 1→2B, 2→4B）；
// offsetBytes 和 offsetCount 由 fre_info 字节的 bits[5:6] 和 bits[1:4] 决定。
// 返回 (字节数, 是否成功)。
func calcFRESize(data []byte, offset int, freType uint8) (int, bool) {
	var addrSize int
	switch freType {
	case 0:
		addrSize = 1
	case 1:
		addrSize = 2
	case 2:
		addrSize = 4
	default:
		return 0, false
	}
	if offset+addrSize+1 > len(data) {
		return 0, false
	}
	freInfo := data[offset+addrSize]
	offsetCount := int((freInfo >> 1) & 0x0F)
	offsetSizeCode := (freInfo >> 5) & 0x03
	var offsetBytes int
	switch offsetSizeCode {
	case SFRAME_FRE_DATAWORD_1B:
		offsetBytes = 1
	case SFRAME_FRE_DATAWORD_2B:
		offsetBytes = 2
	case SFRAME_FRE_DATAWORD_4B:
		offsetBytes = 4
	default:
		return 0, false
	}
	total := addrSize + 1 + offsetCount*offsetBytes
	if offset+total > len(data) {
		return 0, false
	}
	return total, true
}

// sframeComputeFREByteLensByParsing 通过逐条解析 FRE 的字节大小来计算每个 V3 函数的
// FRE 字节范围，作为对比验证手段（与 sframeComputeFREByteLens 差值法结果互相校验）。
//
// 对于 V3：每个函数的 FRE 数据以 5 字节 sframe_func_desc_attr 开头，其中含 NumFREs。
// FREByteLen_parsed = 5(attr) + Σ calcFRESize(每条FRE)
//
// 返回值：与 functions 等长的切片，存储逐条解析得到的 FREByteLen；
// 若某函数解析失败，对应位置为 0。
func sframeComputeFREByteLensByParsing(functions []SFrameFunction, sframe *SFrameData, sectionData []byte) []uint32 {
	result := make([]uint32, len(functions))
	if sframe == nil || sframe.Header.Version < 3 {
		return result
	}
	headerSize := 28 + int(sframe.Header.AuxHdrLen)
	freSubStart := headerSize + int(sframe.Header.FREOff)

	for i := range functions {
		fn := &functions[i]
		attrOff := freSubStart + int(fn.StartFREOff)
		if attrOff+5 > len(sectionData) {
			debugLog("[DEBUG] sframeComputeFREByteLensByParsing: FDE[%d] attr 超出范围\n", i)
			continue
		}
		numFREs := int(uint32(sectionData[attrOff]) | uint32(sectionData[attrOff+1])<<8)
		funcInfo := sectionData[attrOff+2]
		freType := funcInfo & 0x0F

		offset := attrOff + 5 // 跳过 sframe_func_desc_attr
		totalFREBytes := 0
		ok := true
		for j := 0; j < numFREs; j++ {
			sz, valid := calcFRESize(sectionData, offset, freType)
			if !valid {
				debugLog("[DEBUG] sframeComputeFREByteLensByParsing: FDE[%d] FRE[%d] 解析失败, offset=%d\n",
					i, j, offset)
				ok = false
				break
			}
			totalFREBytes += sz
			offset += sz
		}
		if ok {
			result[i] = uint32(5 + totalFREBytes)
		}
	}
	return result
}

// parseFRE 从二进制数据中解析单个 FRE
func parseFRE(data []byte, offset int, freType uint8, abi uint8) (*SFrameFDE, int, error) {
	// freType 决定 start address 字段的大小
	var addrSize int
	var startAddr uint32

	switch freType {
	case 0: // SFRAME_FRE_TYPE_ADDR1
		addrSize = 1
		if offset+1 > len(data) {
			return nil, 0, fmt.Errorf("insufficient data for FRE addr1")
		}
		startAddr = uint32(data[offset])
	case 1: // SFRAME_FRE_TYPE_ADDR2
		addrSize = 2
		if offset+2 > len(data) {
			return nil, 0, fmt.Errorf("insufficient data for FRE addr2")
		}
		startAddr = uint32(binary.LittleEndian.Uint16(data[offset : offset+2]))
	case 2: // SFRAME_FRE_TYPE_ADDR4
		addrSize = 4
		if offset+4 > len(data) {
			return nil, 0, fmt.Errorf("insufficient data for FRE addr4")
		}
		startAddr = binary.LittleEndian.Uint32(data[offset : offset+4])
	default:
		return nil, 0, fmt.Errorf("invalid FRE type: %d", freType)
	}

	offset += addrSize

	// 读取 FRE Info Word (1 字节)
	if offset+1 > len(data) {
		return nil, 0, fmt.Errorf("insufficient data for FRE info")
	}
	freInfo := data[offset]
	offset++

	// 解析 FRE Info Word
	_, offsetSize, offsetCount, _ := parseFREInfo(freInfo)

	// 确定偏移值的字节大小
	var offsetBytes int
	switch offsetSize {
	case SFRAME_FRE_OFFSET_1B:
		offsetBytes = 1
	case SFRAME_FRE_OFFSET_2B:
		offsetBytes = 2
	case SFRAME_FRE_OFFSET_4B:
		offsetBytes = 4
	default:
		return nil, 0, fmt.Errorf("invalid FRE offset size: %d", offsetSize)
	}

	// 读取偏移值
	totalOffsetBytes := int(offsetCount) * offsetBytes
	if offset+totalOffsetBytes > len(data) {
		return nil, 0, fmt.Errorf("insufficient data for FRE offsets")
	}

	// 读取各个偏移值
	offsets := make([]int32, offsetCount)
	for i := 0; i < int(offsetCount); i++ {
		var val int32
		switch offsetBytes {
		case 1:
			val = int32(int8(data[offset]))
			offset++
		case 2:
			val = int32(int16(binary.LittleEndian.Uint16(data[offset : offset+2])))
			offset += 2
		case 4:
			val = int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		offsets[i] = val
		debugLog("offsets[%d]: %d\n", i, val)
	}

	// 构造 FDE，第一个偏移始终是 CFA offset
	fde := &SFrameFDE{
		StartOffset: startAddr,
		FREInfo:     freInfo,
	}
	if offsetCount >= 1 {
		fde.CFAOffset = offsets[0]
	}

	// 根据 ABI 解释剩余偏移
	switch abi {
	case SFRAME_ABI_AMD64_ENDIAN_LITTLE:
		// AMD64: offset1 = CFA, offset2 = FP (如果存在)
		// RA 总是在 CFA-8 (固定)，但 offsetCount==0 表示 RA undefined（最外层帧）
		// 规范 §1.3 Errata 2: offsetCount==0 => RA is undefined, outermost frame reached
		if offsetCount >= 1 {
			fde.RAOffset = -8
		}
		if offsetCount >= 2 {
			fde.FPOffset = offsets[1]
		}
	case SFRAME_ABI_AARCH64_ENDIAN_LITTLE, SFRAME_ABI_AARCH64_ENDIAN_BIG:
		// AArch64: offset1 = CFA, offset2 = RA, offset3 = FP
		if offsetCount >= 2 {
			fde.RAOffset = offsets[1]
		}
		if offsetCount >= 3 {
			fde.FPOffset = offsets[2]
		}
	case SFRAME_ABI_S390X_ENDIAN_BIG:
		// s390x: offset1 = CFA, offset2 = RA, offset3 = FP
		if offsetCount >= 2 {
			fde.RAOffset = offsets[1]
		}
		if offsetCount >= 3 {
			fde.FPOffset = offsets[2]
		}
	default:
		if offsetCount >= 2 {
			fde.RAOffset = offsets[1]
		}
		if offsetCount >= 3 {
			fde.FPOffset = offsets[2]
		}
	}

	return fde, offset, nil
}

// findFDEForFunction 为函数内的特定PC查找对应的FDE（用于栈展开）
// V2/V3 的 FuncInfo 均在 SFrameFunction 中（V2来自FDE Index, V3来自解析后的sframe_func_desc_attr）
func findFDEForFunction(sframeFunc *SFrameFunction, sframeData *SFrameData, pcOffset uint64, sectionData []byte) *SFrameFDE {
	debugLog("[DEBUG] findFDEForFunction: pcOffset=0x%x, FuncInfo=0x%x, FuncInfo2=0x%x, StartFREOff=%d, FuncSize=%d, NumFREs=%d, FREByteLen=%d\n",
		pcOffset, sframeFunc.FuncInfo, sframeFunc.FuncInfo2, sframeFunc.StartFREOff, sframeFunc.Size, sframeFunc.NumFREs, sframeFunc.FREByteLen)

	// 如果sframeData为空或没有FRE，无法用SFrame展开，回退到FP
	if sframeData == nil || sectionData == nil || (sframeFunc.NumFREs == 0 && sframeFunc.FREByteLen == 0) {
		debugLog("[DEBUG] findFDEForFunction: 无FRE数据，返回nil\n")
		return nil
	}

	// V3: 检查 FLEX FDE 类型 (sfda_func_info2 bits 0-4)
	// FLEX 类型的 FRE 数据解释与 DEFAULT 完全不同，目前回退到FP
	if sframeData.Header.Version >= 3 {
		fdeTypeV3 := sframeFunc.FuncInfo2 & 0x1F
		if fdeTypeV3 == SFRAME_FDE_TYPE_FLEX {
			debugLog("[DEBUG] findFDEForFunction: FLEX FDE (sfda_func_info2=0x%x)，无法展开，回退到FP\n",
				sframeFunc.FuncInfo2)
			return nil
		}
		// V3 outermost frame: DEFAULT 类型 + NumFREs=0 表示最外层帧
		if fdeTypeV3 == SFRAME_FDE_TYPE_DEFAULT && sframeFunc.NumFREs == 0 {
			debugLog("[DEBUG] findFDEForFunction: V3 outermost frame (DEFAULT + NumFREs=0)\n")
			return nil // 返回nil通知调用者到达最外层
		}
	}

	// FRE 类型: bits 0-3 of FuncInfo (V2来自FDE Index; V3来自sframe_func_desc_attr)
	freType := sframeFunc.FuncInfo & 0x0F

	// 计算FRE数据的起始位置。
	// V3: sframe_func_desc_attr (5字节) 位于每函数 FRE 数据开头，需跳过。
	// V2: FRE 数据直接从 StartFREOff 起始，无 attr 头。
	headerSize := 28 + int(sframeData.Header.AuxHdrLen)
	freAttrStart := headerSize + int(sframeData.Header.FREOff) + int(sframeFunc.StartFREOff)
	freDataStart := freAttrStart
	if sframeData.Header.Version >= 3 {
		freDataStart = freAttrStart + 5 // 跳过 sframe_func_desc_attr
	}

	debugLog("[DEBUG] findFDEForFunction: freType=%d, freAttrStart=%d, freDataStart=%d\n",
		freType, freAttrStart, freDataStart)

	if freDataStart >= len(sectionData) {
		debugLog("[DEBUG] findFDEForFunction: FRE数据超出范围，返回nil\n")
		return nil
	}

	// 遍历所有 FRE，查找匹配的
	offset := freDataStart
	// 字节结束边界: FREByteLen 覆盖从 freAttrStart 起的全部数据（含V3 attr）
	var freByteEnd int
	if sframeFunc.FREByteLen > 0 {
		freByteEnd = freAttrStart + int(sframeFunc.FREByteLen)
	} else {
		freByteEnd = len(sectionData) // V2 模式：不按字节限制，由 NumFREs 计数控制
	}
	var bestMatch *SFrameFDE
	var num uint32

	for i := uint32(0); ; i++ {
		if sframeFunc.NumFREs > 0 && i >= sframeFunc.NumFREs {
			break
		}
		if offset >= freByteEnd {
			break
		}
		fre, newOffset, err := parseFRE(sectionData, offset, freType, sframeData.Header.ABI)
		if err != nil {
			debugLog("[DEBUG] findFDEForFunction: 解析FRE #%d 失败: %v\n", i, err)
			break
		}

		debugLog("[DEBUG] findFDEForFunction: FRE #%d: StartOffset=0x%x, CFAOffset=%d, RAOffset=%d, FPOffset=%d\n",
			i, fre.StartOffset, fre.CFAOffset, fre.RAOffset, fre.FPOffset)

		// 检查是否匹配
		// FuncInfo bit 4 决定 PC 类型 (0=PCINC, 1=PCMASK)，V2/V3 均如此
		var isMatch bool
		pcType := (sframeFunc.FuncInfo >> 4) & 0x01
		if pcType == SFRAME_FDE_PCTYPE_INC {
			// SFRAME_FDE_PCTYPE_INC: startOffset <= pcOffset 的最后一个 FRE
			isMatch = uint64(fre.StartOffset) <= pcOffset
		} else {
			// SFRAME_FDE_PCTYPE_MASK: PC % REP_BLOCK_SIZE >= FRE_START_ADDR
			if sframeFunc.RepSize > 0 {
				pcInBlock := pcOffset % uint64(sframeFunc.RepSize)
				isMatch = uint64(fre.StartOffset) <= pcInBlock
			}
		}
		if isMatch {
			num = i
			bestMatch = fre
		}

		offset = newOffset
	}

	if bestMatch != nil {
		cfaBaseReg := bestMatch.FREInfo & 0x01
		cfaBaseStr := "SP"
		if cfaBaseReg == SFRAME_FRE_CFA_BASE_REG_FP {
			cfaBaseStr = "FP"
		}
		debugLog("[DEBUG] findFDEForFunction: 找到匹配的FRE#%d, CFA_BASE=%s, CFAOffset=%d, RAOffset=%d, FPOffset=%d\n",
			num, cfaBaseStr, bestMatch.CFAOffset, bestMatch.RAOffset, bestMatch.FPOffset)
		return bestMatch
	}

	debugLog("[DEBUG] findFDEForFunction: 未找到匹配的FRE，返回nil\n")
	return nil
}
