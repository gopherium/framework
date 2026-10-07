// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statfs reads the file system facts of the file or folder at path.
var statfs = unix.Statfs

// networkSystems names each network file system Open refuses, by its Statfs_t.Type magic number.
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
}

// checkFolder returns an error when the file or folder at path is on a network or unreadable file system.
func checkFolder(path string) error {
	var st unix.Statfs_t
	if err := statfs(path, &st); err != nil {
		return fmt.Errorf("dbkit: read the file system of %s: %w", path, err)
	}
	if name, ok := networkSystems[uint32(st.Type)]; ok {
		return fmt.Errorf("dbkit: %s is on a network file system (%s), where SQLite locks are unreliable, "+
			"keep the database on a local disk", path, name)
	}
	return nil
}
