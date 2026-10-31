#ifndef NEWSCACHE_TEST_TEMP_DIR_H
#define NEWSCACHE_TEST_TEMP_DIR_H

#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include <iostream>
#include <string>
#include <vector>

// Each check program owns one private mkdtemp directory. Never follow
// symlinks when removing its contents, and never invoke a shell on TMPDIR.
namespace test_temp {
static std::string directory;
static pid_t owner = 0;

static bool remove_tree(const std::string & path)
{
	DIR *dir = opendir(path.c_str());
	if (!dir)
		return false;
	bool ok = true;
	struct dirent *entry;
	while ((entry = readdir(dir)) != NULL) {
		if (strcmp(entry->d_name, ".") == 0 || strcmp(entry->d_name, "..") == 0)
			continue;
		std::string child = path + "/" + entry->d_name;
		struct stat st;
		if (lstat(child.c_str(), &st) != 0) {
			ok = false;
		} else if (S_ISDIR(st.st_mode)) {
			if (!remove_tree(child))
				ok = false;
		} else if (unlink(child.c_str()) != 0) {
			ok = false;
		}
	}
	if (closedir(dir) != 0)
		ok = false;
	if (rmdir(path.c_str()) != 0)
		ok = false;
	return ok;
}

static void cleanup()
{
	// Children of fork must not remove their parent's fixture on exit.
	if (owner != getpid() || directory.empty())
		return;
	if (!remove_tree(directory))
		std::cerr << "could not remove test directory " << directory << "\n";
	else
		directory.clear();
}

static bool create(const char *name, std::string & result)
{
	const char *tmp = getenv("TMPDIR");
	std::string pattern = (tmp && *tmp ? tmp : "/tmp");
	pattern += "/";
	pattern += name;
	pattern += ".XXXXXX";
	std::vector<char> buf(pattern.begin(), pattern.end());
	buf.push_back('\0');
	if (!mkdtemp(&buf[0])) {
		perror("mkdtemp");
		return false;
	}
	directory = &buf[0];
	owner = getpid();
	if (atexit(cleanup) != 0) {
		cleanup();
		return false;
	}
	result = directory;
	return true;
}
}

#endif
