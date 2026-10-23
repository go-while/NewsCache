#include"NVList.h"
#include"Error.h"

#include<fcntl.h>
#include<sys/mman.h>
#include<sys/stat.h>
#include<sys/types.h>

using namespace std;

int NVList::is_empty()
{
	int r;
	lock(ShrdLock);
	r = sis_empty(getdatap());
	lock(UnLock);
	return r;
}

void NVList::prepend(const char *data, size_t szdata)
{
	NVLock guard(*this, ExclLock);	// (allocates: may throw)
	sprepend(getdatap(), data, szdata);
}

void NVList::append(const char *data, size_t szdata)
{
	NVLock guard(*this, ExclLock);	// (allocates: may throw)
	sappend(getdatap(), data, szdata);
}

void NVList::remove()
{
	if (getdata()) {
		NVLock guard(*this, ExclLock);
		sremove(getdatap());
	}
}

void NVList::clear()
{
	NVLock guard(*this, ExclLock);
	sclear(getdatap());
}

void NVList::print(ostream & os)
{
	lock(ShrdLock);
	sprint(getdatap(), os);
	lock(UnLock);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
