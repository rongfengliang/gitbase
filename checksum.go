package gitbase

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"io"

	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
)

type checksumable struct {
	pool *RepositoryPool
}

func (c *checksumable) Checksum() (string, error) {
	hash := sha1.New()
	for _, id := range c.pool.idOrder {
		repo := c.pool.repositories[id]
		hash.Write([]byte(id))

		bytes, err := readChecksum(repo)
		if err != nil {
			return "", err
		}

		if _, err = hash.Write(bytes); err != nil {
			return "", err
		}

		bytes, err = readRefs(repo)
		if err != nil {
			return "", err
		}

		if _, err = hash.Write(bytes); err != nil {
			return "", err
		}
	}

	return base64.StdEncoding.EncodeToString(hash.Sum(nil)), nil
}

func readChecksum(r repository) ([]byte, error) {
	fs, err := r.FS()
	if err != nil {
		return nil, err
	}

	dot, packfiles, err := repositoryPackfiles(fs)
	if err != nil {
		return nil, err
	}

	var result []byte
	for _, p := range packfiles {
		f, err := dot.ObjectPack(p)
		if err != nil {
			return nil, err
		}

		if _, err = f.Seek(-20, io.SeekEnd); err != nil {
			return nil, err
		}

		var checksum = make([]byte, 20)
		if _, err = io.ReadFull(f, checksum); err != nil {
			return nil, err
		}

		if err = f.Close(); err != nil {
			return nil, err
		}

		result = append(result, checksum...)
	}

	return result, nil
}

func readRefs(r repository) ([]byte, error) {
	repo, err := r.Repo()
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			return nil, nil
		}
		return nil, err
	}

	buf := bytes.NewBuffer(nil)

	head, err := repo.Head()
	if err != nil && err != plumbing.ErrReferenceNotFound {
		return nil, err
	} else {
		buf.WriteString("HEAD")
		buf.WriteString(head.Hash().String())
	}

	refs, err := repo.References()
	if err != nil {
		return nil, err
	}

	err = refs.ForEach(func(r *plumbing.Reference) error {
		buf.WriteString(string(r.Name()))
		buf.WriteString(r.Hash().String())
		return nil
	})
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
