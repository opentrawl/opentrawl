package qa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func createTelegramFixture(root, repoRoot string) error {
	lane := filepath.Join(root, "stable")
	account := filepath.Join(lane, "account-123")
	dbDir := filepath.Join(account, "postbox", "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return err
	}
	keyAndSalt := make([]byte, 48)
	for i := range keyAndSalt {
		keyAndSalt[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(lane, ".tempkeyEncrypted"), encryptedTempKey([]byte("no-matter-key"), keyAndSalt), 0o600); err != nil {
		return err
	}
	src := filepath.Join(repoRoot, "trawlers", "telegram", "internal", "telegramdesktop", "postbox", "testdata", "sqlcipher_v4.db")
	return copyFile(filepath.Join(dbDir, "db_sqlite"), src)
}

func AddTelegramLaunchTerm(home string) error {
	db, err := sql.Open("sqlite3", filepath.Join(home, ".opentrawl", "telegram", "telegram.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	row := `(select rowid from messages order by source_pk limit 1)`
	if _, err := db.Exec(`update messages set text = text || ' launch' where rowid = ` + row); err != nil {
		return fmt.Errorf("update telegram message: %w", err)
	}
	if _, err := db.Exec(`update messages_fts set text = text || ' launch' where rowid = ` + row); err != nil {
		return fmt.Errorf("update telegram fts: %w", err)
	}
	return nil
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func encryptedTempKey(passcode []byte, keyAndSalt []byte) []byte {
	plain := make([]byte, 64)
	copy(plain, keyAndSalt)
	binary.LittleEndian.PutUint32(plain[48:52], uint32(tempkeyMurmur3(keyAndSalt)))
	digest := sha512.Sum512(passcode)
	block, err := aes.NewCipher(digest[:32])
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, digest[48:]).CryptBlocks(out, plain)
	return out
}

func tempkeyMurmur3(data []byte) int32 {
	const seed uint32 = 0xf7ca7fd2
	const c1 uint32 = 0xcc9e2d51
	const c2 uint32 = 0x1b873593
	length := len(data)
	h1 := seed
	roundedEnd := length & 0xfffffffc
	for i := 0; i < roundedEnd; i += 4 {
		k1 := uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
		h1 = (h1 << 13) | (h1 >> 19)
		h1 = h1*5 + 0xe6546b64
	}
	var k1 uint32
	switch length & 3 {
	case 3:
		k1 ^= uint32(data[roundedEnd+2]) << 16
		fallthrough
	case 2:
		k1 ^= uint32(data[roundedEnd+1]) << 8
		fallthrough
	case 1:
		k1 ^= uint32(data[roundedEnd])
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
	}
	h1 ^= uint32(length)
	h1 ^= h1 >> 16
	h1 *= 0x85ebca6b
	h1 ^= h1 >> 13
	h1 *= 0xc2b2ae35
	h1 ^= h1 >> 16
	return int32(h1)
}
