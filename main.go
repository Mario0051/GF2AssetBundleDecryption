/*
 * @Author: nijineko
 * @Date: 2023-09-30 22:03:17
 * @LastEditTime: 2025-07-10 06:32:34
 * @LastEditors: Mario0051
 * @Description: main.go
 * @FilePath: \GF2AssetBundleDecryption\main.go
 */
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nijinekoyo/GF2AssetBundleDecryption/Decryption"
	"github.com/nijinekoyo/GF2AssetBundleDecryption/Decryption/Proto"
	"google.golang.org/protobuf/proto"
)

type CombinedTextEntry struct {
	Id         int64
	Original   string
	Translated string
}

type CombinedTextEntrySlice []CombinedTextEntry

func (s *CombinedTextEntrySlice) UnmarshalJSON(data []byte) error {
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(data, &rawMessages); err != nil {
		return fmt.Errorf("failed to unmarshal top-level array: %w", err)
	}

	*s = make(CombinedTextEntrySlice, 0, len(rawMessages))
	for _, raw := range rawMessages {
		var threeElem [3]json.RawMessage
		if err := json.Unmarshal(raw, &threeElem); err == nil {
			var original, translated string
			if json.Unmarshal(threeElem[1], &original) == nil && json.Unmarshal(threeElem[2], &translated) == nil {
				var ids []int64
				if err := json.Unmarshal(threeElem[0], &ids); err == nil {
					for _, id := range ids {
						*s = append(*s, CombinedTextEntry{Id: id, Original: original, Translated: translated})
					}
					continue
				}

				var id int64
				if err := json.Unmarshal(threeElem[0], &id); err == nil {
					*s = append(*s, CombinedTextEntry{Id: id, Original: original, Translated: translated})
					continue
				}
			}
		}
	}
	return nil
}

func formatEntriesToJSON(data []CombinedTextEntry) ([]byte, error) {
	if len(data) == 0 {
		return []byte("[]"), nil
	}

	var buffer bytes.Buffer
	buffer.WriteString("[\n")

	for i, entry := range data {
		entryArray := []interface{}{entry.Id, entry.Original, entry.Translated}
		itemBytes, err := json.Marshal(entryArray)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal entry %d: %w", i, err)
		}

		buffer.WriteString("\t")
		buffer.Write(itemBytes)

		if i < len(data)-1 {
			buffer.WriteString(",")
		}

		buffer.WriteString("\n")
	}

	buffer.WriteString("]")
	return buffer.Bytes(), nil
}

func formatMixedArrayJSON(data []CombinedTextEntry) ([]byte, error) {
	if len(data) == 0 {
		return []byte("[]"), nil
	}

	type groupKey struct {
		Original   string
		Translated string
	}
	groups := make(map[groupKey][]int64)
	for _, entry := range data {
		if entry.Id != -1 && entry.Translated != "" {
			key := groupKey{Original: entry.Original, Translated: entry.Translated}
			groups[key] = append(groups[key], entry.Id)
		}
	}

	var resultData []interface{}
	handledIDs := make(map[int64]bool)

	for _, entry := range data {
		if entry.Id != -1 && handledIDs[entry.Id] {
			continue
		}

		key := groupKey{Original: entry.Original, Translated: entry.Translated}
		idsInGroup, isGrouped := groups[key]

		if entry.Id != -1 && isGrouped && len(idsInGroup) > 1 {
			sort.Slice(idsInGroup, func(i, j int) bool { return idsInGroup[i] < idsInGroup[j] })
			resultData = append(resultData, []interface{}{idsInGroup, entry.Original, entry.Translated})
			for _, id := range idsInGroup {
				handledIDs[id] = true
			}
		} else {
			resultData = append(resultData, []interface{}{entry.Id, entry.Original, entry.Translated})
			if entry.Id != -1 {
				handledIDs[entry.Id] = true
			}
		}
	}

	var buffer bytes.Buffer
	buffer.WriteString("[\n")
	for i, item := range resultData {
		itemBytes, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal entry %d: %w", i, err)
		}

		buffer.WriteString("\t")
		buffer.Write(itemBytes)

		if i < len(resultData)-1 {
			buffer.WriteString(",")
		}

		buffer.WriteString("\n")
	}
	buffer.WriteString("]")
	return buffer.Bytes(), nil
}

type OriginalToolEntry struct {
	Id      int64  `json:"Id"`
	Content string `json:"Content"`
}

type OriginalToolOutput struct {
	Data []OriginalToolEntry `json:"Data"`
}

func parseLangFileToStruct(filePath string) (*Proto.TextMapTable, error) {
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	if len(fileData) < 4 {
		return nil, fmt.Errorf("file %s is too small to contain a valid header", filePath)
	}
	var skip uint32
	err = binary.Read(bytes.NewReader(fileData[:4]), binary.LittleEndian, &skip)
	if err != nil {
		return nil, fmt.Errorf("failed to read header from %s: %w", err)
	}

	protoData := fileData[skip+4:]
	textMapTable := &Proto.TextMapTable{}
	err = proto.Unmarshal(protoData, textMapTable)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal protobuf data from %s: %w", filePath, err)
	}

	return textMapTable, nil
}

type KVPair struct {
	Key, Value string
}

func main() {
	Model := flag.String("model", "ab", "指定模式，可选值为 ab, story, combine, to-original-tool, to-keyvalue-tool, merge-json")
	AssetBundlePath := flag.String("ab_path", "./AssetBundles_Windows", "指定AssetBundle文件夹路径")
	AssetBundleDecryptedPath := flag.String("ab_decrypted_path", "./ab_decrypted_output", "指定AssetBundle文件解密后文件夹路径")
	TablePath := flag.String("table_path", "./Table", "指定Table文件夹路径")
	TableDecryptedPath := flag.String("table_decrypted_path", "./table_decrypted_output", "指定Table文件解析后文件夹路径")
	MaxPoolNum := flag.Int("max_pool", 20, "指定最大并发数")
	BasePath := flag.String("base_path", "./Table/LangPackageTableCnData.bytes", "原始(中文)剧情文件路径")
	TranslatedPath := flag.String("translated_path", "", "翻译后(英文)剧情文件路径")
	InputCombinedPath := flag.String("input_combined", "./combined_story.json", "输入的合并后JSON文件路径")
	OutputPath := flag.String("output", "./output.json", "输出文件路径")
	BaseJsonPath := flag.String("base", "base.json", "基础JSON文件 (将被更新)")
	OverrideJsonPath := flag.String("override", "override.json", "包含新翻译的覆盖JSON文件")
	flag.Parse()

	switch *Model {
	case "ab":
		err := os.MkdirAll(*AssetBundleDecryptedPath, 0666)
		if err != nil {
			panic(err)
		}

		// 遍历AssetBundle文件夹，找出所有.bundle文件
		var AssetBundleFiles []string
		AllFiles, err := TraverseFolders(*AssetBundlePath)
		if err != nil {
			panic(err)
		}
		// 过滤出.bundle文件
		for _, file := range AllFiles {
			if filepath.Ext(file) == ".bundle" {
				AssetBundleFiles = append(AssetBundleFiles, file)
			}
		}

		Pool := NewPool(*MaxPoolNum)

		// 解密AssetBundle
		for _, FilePath := range AssetBundleFiles {
			Pool.Add(1)

			go func() {
				FileData, err := os.ReadFile(FilePath)
				if err != nil {
					panic(err)
				}

				// 解密
				DecryptedFile := Decryption.AssetBundle(FileData)

				// 写入文件
				DecryptedFilePath := filepath.Join(*AssetBundleDecryptedPath, filepath.Base(FilePath))

				err = os.WriteFile(DecryptedFilePath, DecryptedFile, 0666)
				if err != nil {
					panic(err)
				}

				fmt.Println("解密成功，文件已保存至:", DecryptedFilePath)
				Pool.Done()
			}()

			Pool.Wait()
		}

		os.Exit(0)
	case "story":
		err := os.MkdirAll(*TableDecryptedPath, 0666)
		if err != nil {
			panic(err)
		}

		StoryDataFiles := []string{
			"LangPackageTableCnBuiltinData.bytes",
			"LangPackageTableCnData.bytes",
		}

		// 解析剧情文件
		for _, FilName := range StoryDataFiles {
			// 读取文件
			FileData, err := os.ReadFile(filepath.Join(*TablePath, FilName))
			if err != nil {
				panic(err)
			}

			// 解析
			DecryptedFile, err := Decryption.LangPackageTable(FileData)
			if err != nil {
				panic(err)
			}

			// 写入文件
			DecryptedFilePath := filepath.Join(*TableDecryptedPath, strings.Replace(filepath.Base(FilName), filepath.Ext(FilName), ".json", 1))

			err = os.WriteFile(DecryptedFilePath, DecryptedFile, 0666)
			if err != nil {
				panic(err)
			}

			fmt.Println("解析成功，文件已保存至:", DecryptedFilePath)
		}

		os.Exit(0)
	case "combine":
		fmt.Println("开始执行剧情文件合并模式...")

		fmt.Printf("正在解析原始文件: %s\n", *BasePath)
		baseTable, err := parseLangFileToStruct(*BasePath)
		if err != nil {
			panic(fmt.Errorf("处理原始文件时出错: %w", err))
		}

		translatedTextMap := make(map[int64]string)
		if *TranslatedPath != "" {
			fmt.Printf("正在解析翻译文件: %s\n", *TranslatedPath)
			translatedTable, err := parseLangFileToStruct(*TranslatedPath)
			if err != nil {
				fmt.Printf("警告: 处理翻译文件时出错，将继续但不会包含翻译内容: %v\n", err)
			} else {
				for _, entry := range translatedTable.Data {
					translatedTextMap[entry.Id] = entry.Content
				}
			}
		} else {
			fmt.Println("翻译文件未提供，将生成仅包含原文的JSON。")
		}

		var combinedData []CombinedTextEntry
		for _, baseEntry := range baseTable.Data {
			translatedContent, ok := translatedTextMap[baseEntry.Id]
			if !ok {
				translatedContent = ""
			}
			combinedData = append(combinedData, CombinedTextEntry{
				Id:         baseEntry.Id,
				Original:   baseEntry.Content,
				Translated: translatedContent,
			})
		}

		finalJsonData, err := formatEntriesToJSON(combinedData)
		if err != nil {
			panic(fmt.Errorf("序列化JSON失败: %w", err))
		}

		if err = os.WriteFile(*OutputPath, finalJsonData, 0644); err != nil {
			panic(fmt.Errorf("写入合并后的JSON文件失败: %w", err))
		}
		fmt.Printf("合并成功！共处理 %d 条数据，文件已保存至: %s\n", len(combinedData), *OutputPath)
		os.Exit(0)
	case "to-original-tool":
		fmt.Println("开始转换: 合并JSON -> 原始工具格式...")

		combinedDataBytes, err := os.ReadFile(*InputCombinedPath)
		if err != nil {
			panic(fmt.Errorf("读取合并JSON文件失败: %w", err))
		}

		var combinedData CombinedTextEntrySlice
		if err := json.Unmarshal(combinedDataBytes, &combinedData); err != nil {
			panic(fmt.Errorf("解析合并JSON文件失败: %w", err))
		}

		originalOutput := OriginalToolOutput{}
		translatedOutput := OriginalToolOutput{}

		for _, entry := range combinedData {
			if entry.Id == -1 {
				continue
			}
			originalOutput.Data = append(originalOutput.Data, OriginalToolEntry{Id: entry.Id, Content: entry.Original})
			translatedOutput.Data = append(translatedOutput.Data, OriginalToolEntry{Id: entry.Id, Content: entry.Translated})
		}

		originalJson, _ := json.MarshalIndent(originalOutput, "", "    ")
		translatedJson, _ := json.MarshalIndent(translatedOutput, "", "    ")

		originalOutputPath := strings.Replace(*OutputPath, ".json", "_original.json", 1)
		translatedOutputPath := strings.Replace(*OutputPath, ".json", "_translated.json", 1)

		if err := os.WriteFile(originalOutputPath, originalJson, 0644); err != nil {
			panic(fmt.Errorf("写入原始格式JSON失败: %w", err))
		}
		fmt.Printf("成功生成原始格式文件: %s\n", originalOutputPath)

		if err := os.WriteFile(translatedOutputPath, translatedJson, 0644); err != nil {
			panic(fmt.Errorf("写入翻译格式JSON失败: %w", err))
		}
		fmt.Printf("成功生成翻译格式文件: %s\n", translatedOutputPath)

		os.Exit(0)
	case "to-keyvalue-tool":
		fmt.Println("开始转换: 混合JSON -> 有序键值对...")

		combinedDataBytes, err := os.ReadFile(*InputCombinedPath)
		if err != nil {
			panic(fmt.Errorf("读取合并JSON文件失败: %w", err))
		}

		var combinedData CombinedTextEntrySlice
		if err := json.Unmarshal(combinedDataBytes, &combinedData); err != nil {
			panic(fmt.Errorf("解析混合JSON文件失败: %w", err))
		}

		var orderedPairs []KVPair
		handledOriginals := make(map[string]bool)

		for _, entry := range combinedData {
			if entry.Original == "" {
				continue
			}

			if !handledOriginals[entry.Original] {
				orderedPairs = append(orderedPairs, KVPair{Key: entry.Original, Value: entry.Translated})
				handledOriginals[entry.Original] = true
			}
		}

		var buffer bytes.Buffer
		buffer.WriteString("{\n")
		for i, pair := range orderedPairs {
			keyBytes, _ := json.Marshal(pair.Key)
			valueBytes, _ := json.Marshal(pair.Value)

			buffer.WriteString("\t")
			buffer.Write(keyBytes)
			buffer.WriteString(": ")
			buffer.Write(valueBytes)

			if i < len(orderedPairs)-1 {
				buffer.WriteString(",")
			}
			buffer.WriteString("\n")
		}
		buffer.WriteString("}")
		finalJson := buffer.Bytes()

		if err := os.WriteFile(*OutputPath, finalJson, 0644); err != nil {
			panic(fmt.Errorf("写入有序键值对JSON失败: %w", err))
		}
		fmt.Printf("成功生成有序键值对文件: %s\n", *OutputPath)
		os.Exit(0)
	case "merge-json":
		fmt.Println("开始执行合并JSON文件模式 (按ID排序结构匹配)...")

		baseBytes, err := os.ReadFile(*BaseJsonPath)
		if err != nil {
			panic(fmt.Errorf("读取基础JSON文件失败 (%s): %w", *BaseJsonPath, err))
		}
		overrideBytes, err := os.ReadFile(*OverrideJsonPath)
		if err != nil {
			panic(fmt.Errorf("读取覆盖JSON文件失败 (%s): %w", *OverrideJsonPath, err))
		}

		var baseData CombinedTextEntrySlice
		if err := json.Unmarshal(baseBytes, &baseData); err != nil {
			panic(fmt.Errorf("解析基础JSON文件失败: %w", err))
		}
		var overrideData CombinedTextEntrySlice
		if err := json.Unmarshal(overrideBytes, &overrideData); err != nil {
			panic(fmt.Errorf("解析覆盖JSON文件失败: %w", err))
		}

		baseEntries := []CombinedTextEntry(baseData)
		overrideEntries := []CombinedTextEntry(overrideData)

		baseGroups := make(map[string][]*CombinedTextEntry)
		for i := range baseEntries {
			if baseEntries[i].Id == -1 {
				continue
			}
			entry := &baseEntries[i]
			baseGroups[entry.Original] = append(baseGroups[entry.Original], entry)
		}

		overrideGroups := make(map[string][]*CombinedTextEntry)
		for i := range overrideEntries {
			if overrideEntries[i].Id == -1 {
				continue
			}
			entry := &overrideEntries[i]
			overrideGroups[entry.Original] = append(overrideGroups[entry.Original], entry)
		}

		for originalText, baseGroupEntries := range baseGroups {
			overrideGroupEntries, ok := overrideGroups[originalText]
			if !ok {
				continue
			}

			sort.Slice(baseGroupEntries, func(i, j int) bool {
				return baseGroupEntries[i].Id < baseGroupEntries[j].Id
			})
			sort.Slice(overrideGroupEntries, func(i, j int) bool {
				return overrideGroupEntries[i].Id < overrideGroupEntries[j].Id
			})

			minLen := len(baseGroupEntries)
			if len(overrideGroupEntries) < minLen {
				minLen = len(overrideGroupEntries)
			}

			for i := 0; i < minLen; i++ {
				if overrideGroupEntries[i].Translated != "" {
					baseGroupEntries[i].Translated = overrideGroupEntries[i].Translated
				}
			}
		}

		resultJson, err := formatMixedArrayJSON(baseEntries)
		if err != nil {
			panic(fmt.Errorf("序列化最终JSON失败: %w", err))
		}

		if err := os.WriteFile(*OutputPath, resultJson, 0644); err != nil {
			panic(fmt.Errorf("写入输出文件失败: %w", err))
		}

		fmt.Printf("结构化合并成功！文件已保存至: %s\n", *OutputPath)
		os.Exit(0)
	default:
		fmt.Println("未知的模式。请使用 -model ab, story, combine, to-original-tool, 或 to-keyvalue-tool")
	}
}
