// Exercises the production installation worker against an isolated NSIS app ID.
#include "../src/install.h"
#include <iostream>

int wmain(int count, wchar_t** arguments) {
  CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
  if (count == 2 && std::wstring(arguments[1]) == L"--default-dir") {
    std::cout << setup::Utf8(setup::DefaultDirectory(false)) << '\n';
    CoUninitialize();
    return 0;
  }
  if (count < 2) return 2;
  setup::InstallOptions options{arguments[1], false, count > 2 && std::wstring(arguments[2]) == L"--desktop-shortcut"};
  bool complete = false;
  setup::Install(GetModuleHandleW(nullptr), nullptr, options, [&](setup::Update update) {
    if (update.phase == setup::Phase::Preparing && update.fraction < 1) return;
    std::cout << static_cast<int>(update.phase) << ' ' << setup::Utf8(update.message) << '\n';
    if (!update.logPath.empty()) std::cout << "log=" << setup::Utf8(update.logPath) << '\n';
    complete = update.phase == setup::Phase::Complete;
  });
  CoUninitialize();
  return complete ? 0 : 1;
}
