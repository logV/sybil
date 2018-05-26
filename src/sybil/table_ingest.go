package sybil

import "time"
import "path"
import "io/ioutil"

import "os"
import "strings"

// to ingest, make a new tmp file inside ingest/ (or append to an existing one)
// to digest, make a new STOMACHE_DIR tempdir and move all files from ingest/ into it

var MIN_FILES_TO_DIGEST = 0

func (t *Table) getNewIngestBlockName(flags *FlagDefs) (string, error) {
	Debug("GETTING INGEST BLOCK NAME", *flags.DIR, "TABLE", t.Name)
	name, err := ioutil.TempDir(path.Join(*flags.DIR, t.Name), "block")
	return name, err
}

func (t *Table) getNewCacheBlockFile(flags *FlagDefs) (*os.File, error) {
	Debug("GETTING CACHE BLOCK NAME", *flags.DIR, "TABLE", t.Name)
	tableCacheDir := path.Join(*flags.DIR, t.Name, CACHE_DIR)
	os.MkdirAll(tableCacheDir, 0755)

	// this info block needs to be moved once its finished being written to
	file, err := ioutil.TempFile(tableCacheDir, "info")
	return file, err
}

// Go through newRecords list and save all the new records out to a row store
func (t *Table) IngestRecords(flags *FlagDefs, blockname string) {
	Debug("KEY TABLE", t.KeyTable)
	Debug("KEY TYPES", t.KeyTypes)

	t.AppendRecordsToLog(flags, t.newRecords[:], blockname)
	t.newRecords = make(RecordList, 0)
	t.SaveTableInfo(flags, "info")
	t.ReleaseRecords()

	t.MaybeCompactRecords(flags)
}

// TODO: figure out how often we actually do a collation check by storing last
// collation inside a file somewhere
func (t *Table) CompactRecords(flags *FlagDefs) {
	HOLD_MATCHES = true

	t.ResetBlockCache()
	t.DigestRecords(flags)

}

// we compact if:
// we have over X files
// we have over X megabytes of data
// remember, there is no reason to actually read the data off disk
// until we decide to compact
func (t *Table) MaybeCompactRecords(flags *FlagDefs) {
	if *flags.SKIP_COMPACT {
		return
	}

	if t.ShouldCompactRowStore(flags, INGEST_DIR) {
		t.CompactRecords(flags)
	}
}

var NO_MORE_BLOCKS = GROUP_DELIMITER

type AfterRowBlockLoad func(*FlagDefs, string, RecordList)

var FILE_DIGEST_THRESHOLD = 256
var KB = int64(1024)
var SIZE_DIGEST_THRESHOLD = int64(1024) * 2
var MAX_ROW_STORE_TRIES = 20

func (t *Table) ShouldCompactRowStore(flags *FlagDefs, digest string) bool {
	dirname := path.Join(*flags.DIR, t.Name, digest)
	// if the row store dir does not exist, skip the whole function
	_, err := os.Stat(dirname)
	if os.IsNotExist(err) {
		return false
	}

	var file *os.File
	for i := 0; i < LOCK_TRIES; i++ {
		file, err = os.Open(dirname)
		if err != nil {
			Debug("Can't open the ingestion dir", dirname)
			time.Sleep(LOCK_US)
			if i > MAX_ROW_STORE_TRIES {
				return false
			}

			continue
		}
		break
	}

	files, _ := file.Readdir(0)
	MIN_FILES_TO_DIGEST = len(files)

	if len(files) > FILE_DIGEST_THRESHOLD {
		return true
	}

	size := int64(0)
	for _, f := range files {
		size = size + f.Size()
	}

	// compact every MB or so
	return size/KB > SIZE_DIGEST_THRESHOLD

}
func (t *Table) LoadRowStoreRecords(flags *FlagDefs, digest string, afterBlockLoadCb AfterRowBlockLoad) {
	dirname := path.Join(*flags.DIR, t.Name, digest)
	var err error

	// if the row store dir does not exist, skip the whole function
	_, err = os.Stat(dirname)
	if os.IsNotExist(err) {
		if afterBlockLoadCb != nil {
			afterBlockLoadCb(flags, NO_MORE_BLOCKS, nil)
		}

		return
	}

	var file *os.File
	for i := 0; i < LOCK_TRIES; i++ {
		file, err = os.Open(dirname)
		if err != nil {
			Debug("Can't open the ingestion dir", dirname)
			time.Sleep(LOCK_US)
			if i > MAX_ROW_STORE_TRIES {
				return
			}
			continue
		}
		break
	}

	files, _ := file.Readdir(0)
	if t.RowBlock == nil {
		t.RowBlock = &TableBlock{}
		(*t.RowBlock).RecordList = make(RecordList, 0)
		t.RowBlock.Info = &SavedColumnInfo{}
		t.blockMu.Lock()
		t.BlockList[ROW_STORE_BLOCK] = t.RowBlock
		t.blockMu.Unlock()
		t.RowBlock.Name = ROW_STORE_BLOCK
	}

	for _, file := range files {
		filename := file.Name()

		// we can open .gz files as well as regular .db files
		cname := strings.TrimRight(filename, GZIP_EXT)

		if !strings.HasSuffix(cname, ".db") {
			continue
		}

		filename = path.Join(dirname, file.Name())

		records := t.LoadRecordsFromLog(flags, filename)
		if afterBlockLoadCb != nil {
			afterBlockLoadCb(flags, filename, records)
		}
	}

	if afterBlockLoadCb != nil {
		afterBlockLoadCb(flags, NO_MORE_BLOCKS, nil)
	}

}

func LoadRowBlockCB(flags *FlagDefs, digestname string, records RecordList) {
	if digestname == NO_MORE_BLOCKS {
		return
	}

	t := GetTable(*flags.TABLE)
	block := t.RowBlock

	if len(records) > 0 {
		block.RecordList = append(block.RecordList, records...)
		block.Info.NumRecords = int32(len(block.RecordList))
	}

}

var DELETE_BLOCKS = make([]string, 0)

func (t *Table) RestoreUningestedFiles(flags *FlagDefs) {
	if !t.GrabDigestLock(flags) {
		Debug("CANT RESTORE UNINGESTED RECORDS WITHOUT DIGEST LOCK")
		return
	}

	ingestdir := path.Join(*flags.DIR, t.Name, INGEST_DIR)
	os.MkdirAll(ingestdir, 0777)

	digesting := path.Join(*flags.DIR, t.Name)
	file, _ := os.Open(digesting)
	dirs, _ := file.Readdir(0)

	for _, dir := range dirs {
		if strings.HasPrefix(dir.Name(), STOMACHE_DIR) && dir.IsDir() {
			fname := path.Join(digesting, dir.Name())
			file, _ := os.Open(fname)
			files, _ := file.Readdir(0)
			for _, file := range files {
				Debug("RESTORING UNINGESTED FILE", file.Name())
				from := path.Join(fname, file.Name())
				to := path.Join(ingestdir, file.Name())
				err := RenameAndMod(from, to)
				if err != nil {
					Debug("COULDNT RESTORE UNINGESTED FILE", from, to, err)
				}
			}

			err := os.Remove(path.Join(digesting, dir.Name()))
			if err != nil {
				Debug("REMOVING STOMACHE FAILED!", err)
			}

		}
	}

}

type SaveBlockChunkCB struct {
	digestdir string
}

func (cb *SaveBlockChunkCB) CB(flags *FlagDefs, digestname string, records RecordList) {

	t := GetTable(*flags.TABLE)
	if digestname == NO_MORE_BLOCKS {
		if len(t.newRecords) > 0 {
			t.SaveRecordsToColumns(flags)
			t.ReleaseRecords()
		}

		for _, file := range DELETE_BLOCKS {
			Debug("REMOVING", file)
			os.Remove(file)
		}

		dir, err := os.Open(cb.digestdir)
		if err == nil {
			contents, err := dir.Readdir(0)

			if err == nil && len(contents) == 0 {
				os.RemoveAll(cb.digestdir)
			}
		}
		t.ReleaseDigestLock(flags)
		return
	}

	Debug("LOADED", len(records), "FOR DIGESTION FROM", digestname)
	if len(records) > 0 {
		t.newRecords = append(t.newRecords, records...)
	}
	DELETE_BLOCKS = append(DELETE_BLOCKS, digestname)

}

var STOMACHE_DIR = "stomache"

// Go through rowstore and save records out to column store
func (t *Table) DigestRecords(flags *FlagDefs) {
	canDigest := t.GrabDigestLock(flags)

	if !canDigest {
		t.ReleaseInfoLock(flags)
		Debug("CANT GRAB LOCK FOR DIGEST RECORDS")
		return
	}

	dirname := path.Join(*flags.DIR, t.Name)
	digestfile := path.Join(dirname, INGEST_DIR)
	digesting, err := ioutil.TempDir(dirname, STOMACHE_DIR)

	// TODO: we need to figure a way out such that the STOMACHE_DIR isn't going
	// to ruin us if it still exists (bc some proc didn't clean up after itself)
	if err != nil {
		t.ReleaseDigestLock(flags)
		Debug("ERROR CREATING DIGESTION DIR", err)
		time.Sleep(time.Millisecond * 50)
		return
	}

	file, _ := os.Open(digestfile)

	files, err := file.Readdir(0)
	if len(files) < MIN_FILES_TO_DIGEST {
		Debug("SKIPPING DIGESTION, NOT AS MANY FILES AS WE THOUGHT", len(files), "VS", MIN_FILES_TO_DIGEST)
		t.ReleaseDigestLock(flags)
		return
	}

	if err == nil {
		for _, f := range files {
			RenameAndMod(path.Join(digestfile, f.Name()), path.Join(digesting, f.Name()))
		}
		// We don't want to leave someone without a place to put their
		// ingestions...
		os.MkdirAll(digestfile, 0777)
		basename := path.Base(digesting)
		cb := SaveBlockChunkCB{digesting}
		t.LoadRowStoreRecords(flags, basename, cb.CB)
	} else {
		t.ReleaseDigestLock(flags)
	}
}
