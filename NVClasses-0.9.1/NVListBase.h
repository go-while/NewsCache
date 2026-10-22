#ifndef _NVListBase_h_
#define _NVListBase_h_

#include"NVcontainer.h"

#include<iostream>

/**
 * \class NVListBase
 * \author Thomas Gschwind
 *
 * Base class of NVList and NVHash: singly linked lists of records in an
 * NVcontainer, addressed by their offset (root pointer at proot).
 * Called NVlist up to 1.3.19; renamed because NVlist.h and NVList.h
 * clash on case-insensitive file systems.
 */
class NVListBase:public NVcontainer {
  protected:
	NVListBase(void):NVcontainer()
	{ }

	NVListBase(const char *dbname, int flags = 0)
		: NVcontainer(dbname, flags)
	{ }

	struct Record {
		unsigned long next;	/* 64 */
		unsigned long szdata;	/* 64 */
		// Data follows here. A flexible array member (a GCC/Clang
		// extension in C++) adds nothing to sizeof(Record), so the
		// file layout is unchanged, and the compiler knows the data
		// isn't a zero-size object.
		char data[];
		char *datap() {
			return data;
		}
	};
	static_assert(sizeof(Record) == 2 * sizeof(unsigned long),
		      "Record is part of the spool file format");

	// The record at offset o, checked: o and the record's size come
	// from the file.
	Record *o2r(nvoff_t o) {
		size_t bsz;

		if (!o)
			return NULL;
		if (!valid_block(o, &bsz) || bsz < sizeof(Record) ||
		    ((Record *) (mem_p + o))->szdata > bsz - sizeof(Record))
			corrupt("bad record in a chain");
		return (Record *) (mem_p + o);
	}
	// Counts the steps along a record chain: one longer than the file
	// has room for runs in a loop.
	void chain_step(unsigned long *steps) {
		if (++*steps > mem_sz / sizeof(Record))
			corrupt("loop in a record chain");
	}
	nvoff_t r2o(Record * r) {
		return r ? (char *) r - mem_p : 0;
	}

	int sis_empty(nvoff_t proot) {
		return *(nvoff_t *) (mem_p + proot) == 0;
	}
	void sprepend(nvoff_t proot, const char *data, size_t szdata);
	void sappend(nvoff_t proot, const char *data, size_t szdata);
	void sremove(nvoff_t proot);
	void sclear(nvoff_t proot);
	void sprint(nvoff_t proot, std::ostream & os);
};

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
