## Process this file with automake to produce Makefile.in

## Libraries
noinst_LIBRARIES = libnvcontainer.a

libnvcontainer_a_SOURCES =	Error.h\
	Logger.cc\
	Logger.h\
	NVArray.cc\
	NVArray.h\
	NVHash.cc\
	NVHash.h\
	NVList.cc\
	NVList.h\
	NVcontainer.cc\
	NVcontainer.h\
	NVListBase.cc\
	NVListBase.h\
	Debug.h

#EXTRA_DIST = checks
#
#CXXFLAGS=-Wall $(DEBUG) -fhandle-exceptions $(INCLUDE)
#
#SRC=*.cc
#
#OBJS=Logger.o NVcontainer.o NVListBase.o NVList.o NVHash.o NVArray.o
#
#all: nvcontainer.a
#
#depend:
#	gcc $(INCLUDE) -MM $(SRC) >.depend
#
#nvcontainer.a: $(OBJS)
#	rm -f $@
#	$(AR) rcs $@ $(OBJS)
#
#clean:
#	rm -f nvcontainer.a $(OBJS)
