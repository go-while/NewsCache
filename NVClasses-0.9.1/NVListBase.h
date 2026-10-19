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
		// Data follows here
		char *datap() {
			return ((char *) this) + sizeof(Record);
		}
	};

	Record *o2r(nvoff_t o) {
		return (Record *) (o ? mem_p + o : NULL);
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
