#include"NVListBase.h"
#include"Error.h"

#include<fcntl.h>
#include<sys/mman.h>
#include<sys/stat.h>
#include<sys/types.h>

using namespace std;

void NVListBase::sprepend(nvoff_t proot, const char *data, size_t szdata)
{
	need_mapping();
	Record *tail;
	Record *nr;
	// A new block: its record isn't filled in yet, so not o2r. (nvalloc
	// may move the mapping: mem_p after it.)
	nvoff_t no = nvalloc(szdata + sizeof(Record));
	nr = (Record *) (mem_p + no);
	tail = o2r(*(nvoff_t *) (mem_p + proot));

	nr->szdata = szdata;
	memcpy(nr->datap(), data, szdata);
	if (tail) {
		nr->next = tail->next;
		tail->next = r2o(nr);
	} else {
		*(nvoff_t *) (mem_p + proot) = nr->next = r2o(nr);
	}
}

void NVListBase::sappend(nvoff_t proot, const char *data, size_t szdata)
{
	need_mapping();
	Record *tail;
	Record *nr;
	// A new block: its record isn't filled in yet, so not o2r. (nvalloc
	// may move the mapping: mem_p after it.)
	nvoff_t no = nvalloc(szdata + sizeof(Record));
	nr = (Record *) (mem_p + no);
	tail = o2r(*(nvoff_t *) (mem_p + proot));

	nr->szdata = szdata;
	memcpy(nr->datap(), data, szdata);
	if (tail) {
		nr->next = tail->next;
		*(nvoff_t *) (mem_p + proot) = tail->next = r2o(nr);
	} else {
		*(nvoff_t *) (mem_p + proot) = nr->next = r2o(nr);
	}
}

void NVListBase::sremove(nvoff_t proot)
{
	need_mapping();
	Record *tail = o2r(*(nvoff_t *) (mem_p + proot));
	Record *nr = o2r(tail->next);

	if (tail == nr) {
		*(nvoff_t *) (mem_p + proot) = 0;
	} else {
		tail->next = nr->next;
	}
	nvfree(r2o(nr));
}

void NVListBase::sclear(nvoff_t proot)
{
	need_mapping();
	while (*(nvoff_t *) (mem_p + proot))
		sremove(proot);
}

void NVListBase::sprint(nvoff_t proot, ostream & os)
{
	int i = 0;
	unsigned long steps = 0;
	Record *head = o2r(*(nvoff_t *) (mem_p + proot));
	Record *r = head;

	while (r) {
		chain_step(&steps);
		r = o2r(r->next);
		os << i++ << ": " << r2o(r) << "(" << r->
		    szdata << "): " << r->datap() << endl;
		if (r == head)
			r = NULL;
	}
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
