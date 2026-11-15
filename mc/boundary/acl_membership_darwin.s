//go:build darwin

#include "textflag.h"

TEXT libc_mbr_uuid_to_id_trampoline<>(SB),NOSPLIT,$0-0
	JMP libc_mbr_uuid_to_id(SB)
GLOBL ·libcMBRUUIDToIDAddr(SB), RODATA, $8
DATA ·libcMBRUUIDToIDAddr(SB)/8, $libc_mbr_uuid_to_id_trampoline<>(SB)
