#pragma once
#include <windows.h>
#include <filesystem>
#include <functional>
#include <string>

namespace setup {
enum class Phase { Preparing, Installing, Complete, Failed };
struct InstallOptions {
  std::wstring directory;
  bool allUsers = false;
  bool desktopShortcut = true;
};
struct Update {
  Phase phase;
  std::wstring message;
  std::wstring logPath;
  float fraction = -1.0f;
};

std::string Utf8(const std::wstring& value);
std::wstring Wide(const std::string& value);
std::wstring WindowsError(DWORD code);
std::wstring QuoteArgument(const std::wstring& value);
std::wstring DefaultDirectory(bool allUsers);
std::wstring ValidateDirectory(const std::wstring& value);
std::wstring InstallerArguments(const InstallOptions& options, const std::wstring& stageLog);
bool AppIsRunning();
bool HasPayload(HINSTANCE instance);
void Install(HINSTANCE instance, HWND owner, InstallOptions options, const std::function<void(Update)>& notify);
bool SelfTest(HINSTANCE instance, const std::filesystem::path& report);
}  // namespace setup
