// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statfs reads the file system facts of the file or folder at path.
var statfs = unix.Statfs

// networkSystems names each network file system Open and Snapshot refuse, by its Statfs_t.Type magic number.
var networkSystems = map[uint32]string{
	unix.NFS_SUPER_MAGIC:   "NFS",
	unix.SMB_SUPER_MAGIC:   "SMB",
	unix.CIFS_SUPER_MAGIC:  "CIFS",
	unix.SMB2_SUPER_MAGIC:  "SMB2",
	unix.V9FS_MAGIC:        "9P",
	unix.AFS_SUPER_MAGIC:   "AFS",
	unix.AFS_FS_MAGIC:      "AFS",
	unix.CODA_SUPER_MAGIC:  "Coda",
	unix.NCP_SUPER_MAGIC:   "NCP",
	unix.CEPH_SUPER_MAGIC:  "Ceph",
	unix.OCFS2_SUPER_MAGIC: "OCFS2",
	0x786F4256:             "vboxsf",
	0x7C7C6673:             "prl_fs",
	0x19830326:             "BeeGFS",
	0x0BD00BD0:             "Lustre",
	0x47504653:             "GPFS",
	0x01161970:             "GFS2",
	0x20030528:             "OrangeFS",
	0xAAD7AAEA:             "Panasas",
	0xBACBACBC:             "vmhgfs",
	0x013111A8:             "IBRIX",
	0xBEEFDEAD:             "StorNext",
	0x61636673:             "ACFS",
}

// systemType returns the Statfs_t.Type magic number of the file system of the file or folder at path.
func systemType(path string) (uint32, error) {
	var st unix.Statfs_t
	err := statfs(path, &st)
	return uint32(st.Type), err
}

// checkFolder returns an error when the file or folder at path is on a network or unreadable file system.
func checkFolder(path string) error {
	magic, err := systemType(path)
	if err != nil {
		return fmt.Errorf("dbkit: read the file system of %s: %w", path, err)
	}
	if name, ok := networkSystems[magic]; ok {
		return fmt.Errorf("dbkit: %s is on a network file system (%s), where SQLite locks are unreliable, "+
			"keep the database on a local disk", path, name)
	}
	return nil
}

// snapshotSystem returns the name of the network or FUSE file system of folder, or "" for any other.
func snapshotSystem(folder string) (string, error) {
	magic, err := systemType(folder)
	switch {
	case err != nil:
		return "", err
	case magic == unix.FUSE_SUPER_MAGIC:
		return "FUSE", nil
	}
	return networkSystems[magic], nil
}

// freeSpace returns the bytes the file system of path leaves free to a user who is not root.
func freeSpace(path string) (int64, error) {
	var st unix.Statfs_t
	if err := statfs(path, &st); err != nil {
		return 0, err
	}
	size := uint64(st.Frsize)
	if size == 0 {
		size = uint64(st.Bsize)
	}
	return blockBytes(st.Bavail, size), nil
}
