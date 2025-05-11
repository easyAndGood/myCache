package persistence

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const HeaderSize = 20 // (32*3+64)/8

type Entry struct {
	Key       []byte
	Value     []byte
	KeySize   uint32
	ValueSize uint32
	Mark      uint32
	Timestamp uint64
}

var (
	DataFileName       = "append.data"
	MergeFileName      = "append.data.merge"
	DataBackupFileName = "append.data.bak"
)

const (
	PUT = iota
	DEL
)

func NewEntry(Key, Value []byte, Mark uint32, Timestamp uint64) *Entry {
	result := &Entry{
		Key:       Key,
		Value:     Value,
		KeySize:   uint32(len(Key)),
		ValueSize: uint32(len(Value)),
		Mark:      Mark,
		Timestamp: Timestamp,
	}
	return result
}

func (entry *Entry) Size() uint64 {
	return uint64(entry.KeySize + entry.ValueSize + HeaderSize)
}

func (entry *Entry) Encode() []byte {
	result := make([]byte, entry.Size())
	binary.BigEndian.PutUint32(result[:4], entry.KeySize)
	binary.BigEndian.PutUint32(result[4:8], entry.ValueSize)
	binary.BigEndian.PutUint32(result[8:12], entry.Mark)
	binary.BigEndian.PutUint64(result[12:HeaderSize], entry.Timestamp)
	copy(result[HeaderSize:HeaderSize+entry.KeySize], entry.Key)
	copy(result[HeaderSize+entry.KeySize:entry.Size()], entry.Value)
	return result
}

func Decode(date []byte) (*Entry, error) {
	date_size := len(date)
	if date_size < HeaderSize {
		return nil, errors.New("invalid data")
	}
	KeySize := binary.BigEndian.Uint32(date[:4])
	ValueSize := binary.BigEndian.Uint32(date[4:8])
	Mark := binary.BigEndian.Uint32(date[8:12])
	Timestamp := binary.BigEndian.Uint64(date[12:HeaderSize])

	return &Entry{
			KeySize:   KeySize,
			ValueSize: ValueSize,
			Mark:      Mark,
			Timestamp: Timestamp},
		nil
}

type DatabaseFile struct {
	File   *os.File
	offset int64 // 偏移量
	Pool   *sync.Pool
	mutex  sync.RWMutex
}

func new(path_file string) (*DatabaseFile, error) {
	fmt.Println("try open file ", path_file)
	file, err := os.OpenFile(path_file, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Println("new() error: open file ", err)
		return nil, err
	}
	file_info, err := os.Stat(path_file)
	if err != nil {
		fmt.Println("new() error: os Stat ", err)
		return nil, err
	}
	pool := &sync.Pool{
		New: func() any {
			return make([]byte, HeaderSize)
		}}
	return &DatabaseFile{
		File:   file,
		offset: file_info.Size(),
		Pool:   pool,
		mutex:  sync.RWMutex{},
	}, nil
}

func NewDataFile(path, fileName string) (*DatabaseFile, error) {
	path_file := ""
	if len(fileName) == 0 {
		path_file = filepath.Join(path, DataFileName)
	} else {
		path_file = filepath.Join(path, fileName)
	}

	return new(path_file)
}

func NewMergeFile(path string) (*DatabaseFile, error) {
	path_file := filepath.Join(path, MergeFileName)
	return new(path_file)
}

func (f *DatabaseFile) Write(entry *Entry) (int64, error) { // 返回entry对应的写入偏移量
	data := entry.Encode()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	offset := f.GetOffset()
	fmt.Println("Write() offset: ", data, offset)
	_, err := f.File.WriteAt(data, offset)
	if err != nil {
		return 0, err
	}
	f.AddOffset(int64(entry.Size()))
	return offset, nil
}

func (f *DatabaseFile) Read(offset int64) (*Entry, error) {
	header := f.Pool.Get().([]byte)
	defer f.Pool.Put(header)
	f.mutex.RLock()
	defer f.mutex.RUnlock()
	_, err := f.File.ReadAt(header, offset)
	if err != nil {
		return nil, err
	}
	entry, err := Decode(header)
	if err != nil {
		return nil, err
	}
	key := make([]byte, entry.KeySize)
	_, err = f.File.ReadAt(key, offset+HeaderSize)
	if err != nil {
		return nil, err
	}
	value := make([]byte, entry.ValueSize)
	_, err = f.File.ReadAt(value, offset+int64(HeaderSize+entry.KeySize)) // 从偏移量开始读取entry.ValueSize个字节
	if err != nil {
		return nil, err
	}
	entry.Key = key
	entry.Value = value
	return entry, nil
}

func (f *DatabaseFile) Close() error {
	return f.File.Close()
}

func (f *DatabaseFile) IsOffsetEqual(offset int64) bool {
	return atomic.LoadInt64(&f.offset) == offset
}

func (f *DatabaseFile) UpdateOffset(offset int64) {
	atomic.StoreInt64(&f.offset, offset)
}

func (f *DatabaseFile) AddOffset(delta int64) int64 {
	return atomic.AddInt64(&f.offset, delta) // 返回新值
}

func (f *DatabaseFile) GetOffset() int64 {
	return atomic.LoadInt64(&f.offset)
}

// 将数据顺序写入到磁盘的操作封装
type WriteSequence struct {
	index        sync.Map      // 索引，string key -> int64 offset
	dataPath     string        // 数据文件路径
	databaseFile *DatabaseFile // 数据文件
	mutex        sync.RWMutex
}

func (w *WriteSequence) loadIndex() error {
	if w.databaseFile == nil {
		return errors.New("database file is nil")
	}
	var offset int64 = 0
	for !w.databaseFile.IsOffsetEqual(offset) {
		entry, err := w.databaseFile.Read(offset)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		fmt.Println("load index offset : ", offset, string(entry.Key), entry.Mark)
		if entry.Mark == DEL {
			w.index.Delete(string(entry.Key))
		} else {
			w.index.Store(string(entry.Key), offset)
		}
		offset += int64(entry.Size())
	}
	return nil
}

func NewWriteSequence(dir_path, backup_file string) (*WriteSequence, error) {
	_, err := os.Stat(dir_path)
	if os.IsNotExist(err) {
		err = os.MkdirAll(dir_path, os.ModePerm)
	}
	if err != nil {
		return nil, err
	}
	dir_path_abs, err := filepath.Abs(dir_path)
	if err != nil {
		return nil, err
	}
	if len(backup_file) != 0 {
		data_file_abs := filepath.Join(dir_path_abs, DataFileName)
		backup_file_abs, err := filepath.Abs(backup_file)
		if err != nil {
			return nil, err
		}
		fmt.Println("data_file_abs ", data_file_abs, " backup_file_abs ", backup_file_abs)
		if data_file_abs != backup_file_abs {
			file1, err1 := os.Open(backup_file_abs) // 打开备份文件
			if err1 != nil {
				return nil, err1
			}
			if _, err := os.Stat(data_file_abs); !os.IsNotExist(err) { // 目标文件已存在，则重命名
				timestamp := time.Now().UnixMilli()
				new_file_path := fmt.Sprintf("%s.temp.%d", data_file_abs, timestamp)
				if err := os.Rename(data_file_abs, new_file_path); err != nil {
					return nil, err
				}
			}
			file2, err2 := os.OpenFile(data_file_abs, os.O_RDWR|os.O_CREATE, os.ModePerm) // 创建目标文件
			if err2 != nil {
				return nil, err2
			}
			// 使用结束关闭文件
			defer file1.Close()
			defer file2.Close()
			_, err := io.Copy(file2, file1)
			if err != nil {
				return nil, err
			}
		}
	}
	df, err := NewDataFile(dir_path_abs, "")
	if err != nil {
		return nil, err
	}
	w := &WriteSequence{
		index:        sync.Map{},
		dataPath:     dir_path_abs,
		databaseFile: df,
		mutex:        sync.RWMutex{},
	}

	err = w.loadIndex()
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (w *WriteSequence) Put(key, value []byte) error {
	now := time.Now()
	timestamp := now.UnixMilli() // 毫秒时间戳
	fmt.Println(w.dataPath, " Put() ", key)
	entry := NewEntry(key, value, PUT, uint64(timestamp))
	w.mutex.Lock()
	defer w.mutex.Unlock()
	offset, err := w.databaseFile.Write(entry)
	if err != nil {
		return err
	}
	w.index.Store(string(key), offset)
	return nil
}

func (w *WriteSequence) IsKeyExist(key []byte) (int64, bool) {
	offset, exist := w.index.Load(string(key))
	if !exist {
		return 0, exist
	}
	offset_int64, ok := offset.(int64)
	return offset_int64, ok
}

func (w *WriteSequence) Get(key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("key is nil")
	}
	w.mutex.RLock()
	defer w.mutex.RUnlock()
	offset, exist := w.IsKeyExist(key)
	if !exist {
		return nil, errors.New("key not exist")
	}
	entry, err := w.databaseFile.Read(offset)
	if err != nil {
		return nil, err
	}
	return entry.Value, nil
}

func (w *WriteSequence) Delete(key []byte) error {
	if len(key) == 0 {
		return errors.New("key is nil")
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, exist := w.IsKeyExist(key)
	if !exist {
		return nil
	}
	now := time.Now()
	timestamp := now.UnixMilli() // 毫秒时间戳
	entry := NewEntry(key, nil, DEL, uint64(timestamp))
	_, err := w.databaseFile.Write(entry)
	if err != nil {
		return err
	}
	w.index.Delete(string(key))
	return nil
}

func (w *WriteSequence) Merge() error {
	if w.databaseFile.IsOffsetEqual(0) {
		return nil
	}
	merge_file, err := NewMergeFile(w.dataPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(merge_file.File.Name())
	}()
	err = nil
	new_index := sync.Map{} // 索引，string key -> int64 offset
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.index.Range(func(k, v any) bool {
		key, ok1 := k.(string)
		value, ok2 := v.(int64)
		if !ok1 || !ok2 {
			err = errors.New("type assert error")
			return false
		}
		entry, err := w.databaseFile.Read(value)
		if err != nil {
			err = fmt.Errorf("read entry error: %w", err)
			return false
		}
		new_offset, err := merge_file.Write(entry)
		if err != nil {
			err = fmt.Errorf("write merge file error: %w", err)
			return false
		}
		new_index.Store(key, new_offset)
		return true
	})
	if err != nil {
		return err
	}
	_ = w.databaseFile.Close()

	backup_file := filepath.Join(w.dataPath, DataBackupFileName)
	_ = os.Rename(w.databaseFile.File.Name(), backup_file) // 备份旧文件

	_ = merge_file.File.Close()

	_ = os.Rename(merge_file.File.Name(), w.databaseFile.File.Name())

	new_file, err := NewDataFile(w.dataPath, "")
	if err != nil {
		_ = os.Rename(backup_file, w.databaseFile.File.Name()) // 恢复备份文件
		return err
	}

	w.databaseFile = new_file
	w.index = new_index
	_ = os.Remove(backup_file) // 删除文件

	return nil
}

func (w *WriteSequence) Backup(backupFileName string) error {
	if len(backupFileName) == 0 {
		timestamp := time.Now().UnixMilli() // 获取当前时间戳（毫秒）
		timestampStr := fmt.Sprintf("%s.%d", DataFileName, timestamp)
		backupFileName = filepath.Join(w.dataPath, timestampStr)
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	backupFile, err := os.OpenFile(backupFileName, os.O_RDWR|os.O_CREATE, os.ModePerm) // 创建目标文件
	if err != nil {
		return err
	}
	defer backupFile.Close() // 使用结束关闭文件
	n, err := io.Copy(backupFile, w.databaseFile.File)
	if err != nil {
		return err
	}
	fmt.Println(backupFileName, "backup success, size:", n)
	return nil
}

func (w *WriteSequence) GetIndexSize() int64 {
	var result int64
	w.index.Range(func(_, _ any) bool {
		result++
		return true
	})
	return result
}

func (w *WriteSequence) GetAllIndexKeys() []string {
	result := make([]string, 0, w.GetIndexSize())
	w.index.Range(func(k, _ any) bool {
		key, ok := k.(string)
		if ok {
			result = append(result, key)
		}
		return true
	})
	return result
}

func (w *WriteSequence) Close() error {
	fmt.Println("close write sequence")
	return w.databaseFile.Close()
}
