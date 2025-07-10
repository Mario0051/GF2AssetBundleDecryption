/*
 * @Author: nijineko
 * @Date: 2023-09-30 22:03:17
 * @LastEditTime: 2025-07-10 13:17:23
 * @LastEditors: Mario0051
 * @Description: main.go
 * @FilePath: \GF2AssetBundleDecryption\main.go
 */
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

func parseAsDefaultJSON(bytes []byte) ([]CombinedTextEntry, error) {
	var data []CombinedTextEntry
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(bytes, &rawMessages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal as default json: %w", err)
	}
	for _, raw := range rawMessages {
		var threeElem [3]json.RawMessage
		if err := json.Unmarshal(raw, &threeElem); err == nil {
			var original, translated string
			if json.Unmarshal(threeElem[1], &original) == nil && json.Unmarshal(threeElem[2], &translated) == nil {
				var ids []int64
				if json.Unmarshal(threeElem[0], &ids) == nil {
					for _, id := range ids {
						data = append(data, CombinedTextEntry{Id: id, Original: original, Translated: translated})
					}
					continue
				}
				var id int64
				if json.Unmarshal(threeElem[0], &id) == nil {
					data = append(data, CombinedTextEntry{Id: id, Original: original, Translated: translated})
					continue
				}
			}
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("no valid entries found in default JSON format")
	}
	return data, nil
}

func parseAsDictJSON(bytes []byte) ([]CombinedTextEntry, error) {
	var topLevel [2]json.RawMessage
	if err := json.Unmarshal(bytes, &topLevel); err != nil {
		return nil, fmt.Errorf("failed to unmarshal as keyless dict format: %w", err)
	}
	var dict []string
	if err := json.Unmarshal(topLevel[0], &dict); err != nil {
		return nil, err
	}
	var dataEntries [][]json.RawMessage
	if err := json.Unmarshal(topLevel[1], &dataEntries); err != nil {
		return nil, err
	}
	var entries []CombinedTextEntry
	for _, keylessEntry := range dataEntries {
		if len(keylessEntry) != 3 {
			continue
		}
		var ids []int64
		var o_idx, t_idx int
		if err := json.Unmarshal(keylessEntry[0], &ids); err != nil {
			var singleID int64
			if err2 := json.Unmarshal(keylessEntry[0], &singleID); err2 == nil {
				ids = []int64{singleID}
			} else {
				continue
			}
		}
		if err := json.Unmarshal(keylessEntry[1], &o_idx); err != nil {
			continue
		}
		if err := json.Unmarshal(keylessEntry[2], &t_idx); err != nil {
			continue
		}
		if o_idx >= len(dict) || t_idx >= len(dict) {
			continue
		}
		original := dict[o_idx]
		translated := dict[t_idx]
		for _, id := range ids {
			entries = append(entries, CombinedTextEntry{Id: id, Original: original, Translated: translated})
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no valid entries found in dictionary format")
	}
	return entries, nil
}

func readCombinedFile(filePath string) ([]CombinedTextEntry, error) {
	bytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if entries, err := parseAsDictJSON(bytes); err == nil {
		return entries, nil
	}
	return parseAsDefaultJSON(bytes)
}

func getSquashedData(data []CombinedTextEntry) []interface{} {
	type groupKey struct{ Original, Translated string }
	groups := make(map[groupKey][]int64)
	var orderedKeys []groupKey

	for _, entry := range data {
		if entry.Id == -1 {
			continue
		}
		key := groupKey{Original: entry.Original, Translated: entry.Translated}
		if _, exists := groups[key]; !exists {
			orderedKeys = append(orderedKeys, key)
		}
		groups[key] = append(groups[key], entry.Id)
	}

	var resultData []interface{}
	for _, key := range orderedKeys {
		ids := groups[key]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var idValue interface{} = ids
		if len(ids) == 1 {
			idValue = ids[0]
		}
		resultData = append(resultData, []interface{}{idValue, key.Original, key.Translated})
	}
	return resultData
}

func formatRegularJSON(squashed bool, data []CombinedTextEntry) ([]byte, error) {
	var entriesToMarshal []interface{}
	if squashed {
		entriesToMarshal = getSquashedData(data)
	} else {
		var unsquashedData []interface{}
		for _, entry := range data {
			unsquashedData = append(unsquashedData, []interface{}{entry.Id, entry.Original, entry.Translated})
		}
		entriesToMarshal = unsquashedData
	}

	if len(entriesToMarshal) == 0 {
		return []byte("[]"), nil
	}

	var buffer bytes.Buffer
	buffer.WriteString("[\n")

	for i, item := range entriesToMarshal {
		// Marshal the inner array to a single line
		lineBytes, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal regular JSON entry %d: %w", i, err)
		}

		// Add indentation
		buffer.WriteString("\t")
		buffer.Write(lineBytes)

		// Add a comma if it's not the last item
		if i < len(entriesToMarshal)-1 {
			buffer.WriteString(",")
		}
		buffer.WriteString("\n")
	}

	buffer.WriteString("]")
	return buffer.Bytes(), nil
}

func formatDictionaryJSON(squashed bool, data []CombinedTextEntry) ([]byte, error) {
	type translationSet = map[string]struct{}
	originalToTranslations := make(map[string]translationSet)
	translationOrder := make(map[string][]string)
	originalOrder := []string{}
	originalSeen := make(map[string]bool)

	for _, entry := range data {
		if entry.Original == "" {
			continue
		}
		if !originalSeen[entry.Original] {
			originalOrder = append(originalOrder, entry.Original)
			originalSeen[entry.Original] = true
			originalToTranslations[entry.Original] = make(translationSet)
		}
		if _, exists := originalToTranslations[entry.Original][entry.Translated]; !exists {
			originalToTranslations[entry.Original][entry.Translated] = struct{}{}
			translationOrder[entry.Original] = append(translationOrder[entry.Original], entry.Translated)
		}
	}

	dict := []string{}
	stringMap := make(map[string]int)
	addString := func(s string) {
		if _, ok := stringMap[s]; !ok {
			stringMap[s] = len(dict)
			dict = append(dict, s)
		}
	}
	for _, originalText := range originalOrder {
		addString(originalText)
		for _, translatedText := range translationOrder[originalText] {
			addString(translatedText)
		}
	}

	var keylessData [][]interface{}
	if squashed {
		squashedDataResult := getSquashedData(data)
		keylessData = make([][]interface{}, len(squashedDataResult))
		for i, item := range squashedDataResult {
			entrySlice := item.([]interface{})
			idValue := entrySlice[0]
			original := entrySlice[1].(string)
			translated := entrySlice[2].(string)
			keylessData[i] = []interface{}{idValue, stringMap[original], stringMap[translated]}
		}
	} else {
		keylessData = make([][]interface{}, len(data))
		for i, entry := range data {
			o_idx := stringMap[entry.Original]
			t_idx := stringMap[entry.Translated]
			keylessData[i] = []interface{}{entry.Id, o_idx, t_idx}
		}
	}

	var buffer bytes.Buffer
	buffer.WriteString("[\n")

	buffer.WriteString("\t[\n")
	printedInDictSection := make(map[string]bool)
	isFirstEntryInDict := true
	for _, originalText := range originalOrder {
		lineHasContent := false
		var lineBuffer bytes.Buffer

		if !printedInDictSection[originalText] {
			origBytes, _ := json.Marshal(originalText)
			lineBuffer.Write(origBytes)
			printedInDictSection[originalText] = true
			lineHasContent = true
		}

		for _, translatedText := range translationOrder[originalText] {
			if !printedInDictSection[translatedText] {
				if lineHasContent {
					lineBuffer.WriteString(",")
				}
				transBytes, _ := json.Marshal(translatedText)
				lineBuffer.Write(transBytes)
				printedInDictSection[translatedText] = true
				lineHasContent = true
			}
		}

		if lineHasContent {
			if !isFirstEntryInDict {
				buffer.WriteString(",\n")
			}
			buffer.WriteString("\t\t")
			buffer.Write(lineBuffer.Bytes())
			isFirstEntryInDict = false
		}
	}
	buffer.WriteString("\n\t],\n")

	buffer.WriteString("\t[\n")
	if squashed {
		var current_o_idx = -1
		for i, item := range keylessData {
			o_idx := item[1].(int)
			if i > 0 {
				if o_idx == current_o_idx {
					buffer.WriteString(", ")
				} else {
					buffer.WriteString(",\n\t\t")
				}
			} else {
				buffer.WriteString("\t\t")
			}
			itemBytes, err := json.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal squashed data entry: %w", err)
			}
			buffer.Write(itemBytes)
			current_o_idx = o_idx
		}
	} else {
		for i, item := range keylessData {
			itemBytes, err := json.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal data entry %d: %w", i, err)
			}
			buffer.WriteString("\t\t")
			buffer.Write(itemBytes)
			if i < len(keylessData)-1 {
				buffer.WriteString(",")
			}
			buffer.WriteString("\n")
		}
	}
	if len(keylessData) > 0 {
		buffer.WriteString("\n")
	}
	buffer.WriteString("\t]\n")
	buffer.WriteString("]\n")
	return buffer.Bytes(), nil
}

func formatCSV(squashed bool, data []CombinedTextEntry) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)

	// Write header
	if err := writer.Write([]string{"ID(s)", "Original", "Translated"}); err != nil {
		return nil, err
	}

	if squashed {
		squashedData := getSquashedData(data)
		for _, item := range squashedData {
			entrySlice := item.([]interface{})
			var idsStr string
			switch v := entrySlice[0].(type) {
			case []int64:
				strIDs := make([]string, len(v))
				for i, id := range v {
					strIDs[i] = strconv.FormatInt(id, 10)
				}
				idsStr = strings.Join(strIDs, ";")
			case int64:
				idsStr = strconv.FormatInt(v, 10)
			}
			original := entrySlice[1].(string)
			translated := entrySlice[2].(string)
			if err := writer.Write([]string{idsStr, original, translated}); err != nil {
				return nil, err
			}
		}
	} else {
		for _, entry := range data {
			idsStr := strconv.FormatInt(entry.Id, 10)
			if err := writer.Write([]string{idsStr, entry.Original, entry.Translated}); err != nil {
				return nil, err
			}
		}
	}

	writer.Flush()
	return buffer.Bytes(), writer.Error()
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
	Model := flag.String("model", "ab", "指定模式: ab, story, combine, to-original-tool, to-keyvalue-tool, merge-json, merge-csv")
	AssetBundlePath := flag.String("ab_path", "./AssetBundles_Windows", "指定AssetBundle文件夹路径")
	AssetBundleDecryptedPath := flag.String("ab_decrypted_path", "./ab_decrypted_output", "指定AssetBundle文件解密后文件夹路径")
	TablePath := flag.String("table_path", "./Table", "指定Table文件夹路径")
	TableDecryptedPath := flag.String("table_decrypted_path", "./table_decrypted_output", "指定Table文件解析后文件夹路径")
	MaxPoolNum := flag.Int("max_pool", 20, "指定最大并发数")
	BasePath := flag.String("base_path", "./Table/LangPackageTableCnData.bytes", "原始(中文)剧情文件路径")
	TranslatedPath := flag.String("translated_path", "", "翻译后(英文)剧情文件路径")
	InputPath := flag.String("input", "./combined_story.json", "输入的合并后JSON文件路径")
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
		baseTable, err := parseLangFileToStruct(*BasePath)
		if err != nil {
			panic(fmt.Errorf("处理原始文件时出错: %w", err))
		}
		translatedTextMap := make(map[int64]string)
		if *TranslatedPath != "" {
			translatedTable, err := parseLangFileToStruct(*TranslatedPath)
			if err != nil {
				fmt.Printf("警告: 处理翻译文件时出错: %v\n", err)
			} else {
				for _, entry := range translatedTable.Data {
					translatedTextMap[entry.Id] = entry.Content
				}
			}
		}
		var combinedData []CombinedTextEntry
		for _, baseEntry := range baseTable.Data {
			if baseEntry.Content == "" {
				continue
			}
			translatedContent, ok := translatedTextMap[baseEntry.Id]
			if !ok {
				translatedContent = ""
			}
			combinedData = append(combinedData, CombinedTextEntry{Id: baseEntry.Id, Original: baseEntry.Content, Translated: translatedContent})
		}
		finalJsonData, err := formatDictionaryJSON(false, combinedData)
		if err != nil {
			panic(fmt.Errorf("序列化为字典JSON失败: %w", err))
		}
		if err = os.WriteFile(*OutputPath, finalJsonData, 0644); err != nil {
			panic(fmt.Errorf("写入合并后的JSON文件失败: %w", err))
		}
		fmt.Printf("合并成功！共处理 %d 条数据，文件已保存至: %s\n", len(combinedData), *OutputPath)
		os.Exit(0)
	case "to-original-tool":
		fmt.Println("开始转换: 合并JSON -> 原始工具格式...")
		combinedData, err := readCombinedFile(*InputPath)
		if err != nil {
			panic(fmt.Errorf("解析输入文件失败: %w", err))
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
		combinedData, err := readCombinedFile(*InputPath)
		if err != nil {
			panic(fmt.Errorf("解析输入文件失败: %w", err))
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
	case "merge-json", "merge-csv":
		if *Model == "merge-json" {
			fmt.Println("开始执行合并JSON文件模式...")
		} else {
			fmt.Println("开始执行合并CSV文件模式...")
		}
		baseEntries, err := readCombinedFile(*BaseJsonPath)
		if err != nil {
			panic(fmt.Errorf("读取基础文件失败 (%s): %w", *BaseJsonPath, err))
		}
		overrideEntries, err := readCombinedFile(*OverrideJsonPath)
		if err != nil {
			panic(fmt.Errorf("读取覆盖文件失败 (%s): %w", *OverrideJsonPath, err))
		}
		baseGroups := make(map[string][]*CombinedTextEntry)
		for i := range baseEntries {
			entry := &baseEntries[i]
			if entry.Id == -1 {
				continue
			}
			baseGroups[entry.Original] = append(baseGroups[entry.Original], entry)
		}
		overrideGroups := make(map[string][]*CombinedTextEntry)
		for i := range overrideEntries {
			entry := &overrideEntries[i]
			if entry.Id == -1 {
				continue
			}
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

		var resultBytes []byte
		outputFile := *OutputPath
		if *Model == "merge-json" {
			resultBytes, err = formatRegularJSON(true, baseEntries)
		} else {
			resultBytes, err = formatCSV(true, baseEntries)
		}

		if err != nil {
			panic(fmt.Errorf("序列化最终文件失败: %w", err))
		}
		if err := os.WriteFile(outputFile, resultBytes, 0644); err != nil {
			panic(fmt.Errorf("写入输出文件失败: %w", err))
		}
		fmt.Printf("结构化合并成功！文件已保存至: %s\n", outputFile)
		os.Exit(0)
	default:
		fmt.Println("未知的模式。请使用 -model ab, story, combine, to-original-tool, to-keyvalue-tool, merge-json, merge-csv")
	}
}
