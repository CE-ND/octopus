#include "install.h"
#include "../resource.h"
#include "generated_config.h"
#include <bcrypt.h>
#include <sddl.h>
#include <shlobj.h>
#include <shellapi.h>
#include <tlhelp32.h>
#include <algorithm>
#include <array>
#include <fstream>
#include <iomanip>
#include <memory>
#include <sstream>
#include <stdexcept>
#include <vector>

namespace setup {
namespace fs = std::filesystem;
namespace {
class Handle {
 public:
  explicit Handle(HANDLE value = nullptr) : value_(value) {}
  ~Handle() { if (value_ && value_ != INVALID_HANDLE_VALUE) CloseHandle(value_); }
  Handle(const Handle&) = delete;
  Handle& operator=(const Handle&) = delete;
  HANDLE get() const { return value_; }
  bool valid() const { return value_ && value_ != INVALID_HANDLE_VALUE; }
 private:
  HANDLE value_;
};

std::wstring KnownFolder(REFKNOWNFOLDERID id) {
  PWSTR value = nullptr;
  const HRESULT result = SHGetKnownFolderPath(id, KF_FLAG_DONT_VERIFY, nullptr, &value);
  if (FAILED(result)) throw std::runtime_error("无法读取 Windows 用户目录。");
  // Shell owns an allocation sized to the actual string. Read only to its NUL.
  std::wstring directory(value);
  CoTaskMemFree(value);
  return directory;
}

std::wstring Lower(std::wstring value) {
  std::transform(value.begin(), value.end(), value.begin(), towlower);
  while (value.size() > 3 && (value.back() == L'\\' || value.back() == L'/')) value.pop_back();
  return value;
}

bool SamePath(const fs::path& a, const fs::path& b) {
  return Lower(a.lexically_normal().wstring()) == Lower(b.lexically_normal().wstring());
}

std::wstring RegisteredDirectory(bool allUsers) {
  std::array<wchar_t, 32768> directory{};
  DWORD size = static_cast<DWORD>(directory.size() * sizeof(wchar_t));
  if (RegGetValueW(allUsers ? HKEY_LOCAL_MACHINE : HKEY_CURRENT_USER, kRegistryKey,
      L"InstallLocation", RRF_RT_REG_SZ | RRF_SUBKEY_WOW6464KEY, nullptr,
      directory.data(), &size) == ERROR_SUCCESS) return directory.data();
  return {};
}

std::wstring NewGuid() {
  GUID id{};
  if (FAILED(CoCreateGuid(&id))) throw std::runtime_error("无法创建安装会话。");
  wchar_t text[40]{};
  StringFromGUID2(id, text, 40);
  return text;
}

fs::path PrivateSession(const fs::path& root) {
  fs::create_directories(root);
  HANDLE rawToken = nullptr;
  if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &rawToken))
    throw std::runtime_error("无法读取当前用户身份。");
  Handle token(rawToken);
  DWORD bytes = 0;
  GetTokenInformation(token.get(), TokenUser, nullptr, 0, &bytes);
  std::vector<BYTE> data(bytes);
  if (!GetTokenInformation(token.get(), TokenUser, data.data(), bytes, &bytes))
    throw std::runtime_error("无法读取当前用户身份。");
  LPWSTR sid = nullptr;
  if (!ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(data.data())->User.Sid, &sid))
    throw std::runtime_error("无法创建安装目录权限。");
  const std::wstring acl = L"D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + std::wstring(sid) + L")";
  LocalFree(sid);
  PSECURITY_DESCRIPTOR descriptor = nullptr;
  if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(acl.c_str(), SDDL_REVISION_1, &descriptor, nullptr))
    throw std::runtime_error("无法创建安装目录权限。");
  SECURITY_ATTRIBUTES security{sizeof(SECURITY_ATTRIBUTES), descriptor, FALSE};
  const fs::path session = root / NewGuid();
  const bool created = CreateDirectoryW(session.c_str(), &security) != FALSE;
  LocalFree(descriptor);
  if (!created) throw std::runtime_error("无法创建临时安装目录。");
  return session;
}

class Sha256 {
 public:
  Sha256() {
    if (BCryptOpenAlgorithmProvider(&algorithm_, BCRYPT_SHA256_ALGORITHM, nullptr, 0) < 0)
      throw std::runtime_error("无法初始化安装包校验。");
    DWORD size = 0, written = 0;
    if (BCryptGetProperty(algorithm_, BCRYPT_OBJECT_LENGTH, reinterpret_cast<PUCHAR>(&size), sizeof(size), &written, 0) < 0)
      throw std::runtime_error("无法初始化安装包校验。");
    object_.resize(size);
    if (BCryptCreateHash(algorithm_, &hash_, object_.data(), size, nullptr, 0, 0) < 0)
      throw std::runtime_error("无法初始化安装包校验。");
  }
  ~Sha256() { if (hash_) BCryptDestroyHash(hash_); if (algorithm_) BCryptCloseAlgorithmProvider(algorithm_, 0); }
  void Add(const BYTE* data, DWORD bytes) {
    if (BCryptHashData(hash_, const_cast<PUCHAR>(data), bytes, 0) < 0)
      throw std::runtime_error("安装包校验失败。");
  }
  std::string Finish() {
    std::array<BYTE, 32> digest{};
    if (BCryptFinishHash(hash_, digest.data(), static_cast<ULONG>(digest.size()), 0) < 0)
      throw std::runtime_error("安装包校验失败。");
    std::ostringstream result;
    for (BYTE b : digest) result << std::hex << std::setw(2) << std::setfill('0') << static_cast<int>(b);
    return result.str();
  }
 private:
  BCRYPT_ALG_HANDLE algorithm_ = nullptr;
  BCRYPT_HASH_HANDLE hash_ = nullptr;
  std::vector<BYTE> object_;
};

std::string FileHash(const fs::path& file) {
  std::ifstream stream(file, std::ios::binary);
  if (!stream) throw std::runtime_error("无法读取安装后的程序。");
  Sha256 hash;
  std::vector<BYTE> buffer(256 * 1024);
  while (stream) {
    stream.read(reinterpret_cast<char*>(buffer.data()), static_cast<std::streamsize>(buffer.size()));
    if (stream.gcount()) hash.Add(buffer.data(), static_cast<DWORD>(stream.gcount()));
  }
  if (!stream.eof()) throw std::runtime_error("无法校验安装后的程序。");
  return hash.Finish();
}

std::string ReadFile(const fs::path& file) {
  std::ifstream stream(file, std::ios::binary);
  return {std::istreambuf_iterator<char>(stream), std::istreambuf_iterator<char>()};
}

void CleanSession(const fs::path& session, const fs::path& root) {
  // Only remove the two files created by this invocation; never recurse.
  if (session.empty() || !SamePath(session.parent_path(), root)) return;
  DeleteFileW((session / L"payload.exe").c_str());
  DeleteFileW((session / L"stages.log").c_str());
  RemoveDirectoryW(session.c_str());
}
}  // namespace

std::string Utf8(const std::wstring& value) {
  if (value.empty()) return {};
  const int size = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
  std::string result(size, '\0');
  WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), result.data(), size, nullptr, nullptr);
  return result;
}

std::wstring Wide(const std::string& value) {
  if (value.empty()) return {};
  const int size = MultiByteToWideChar(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), nullptr, 0);
  std::wstring result(size, L'\0');
  MultiByteToWideChar(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), result.data(), size);
  return result;
}

std::wstring WindowsError(DWORD code) {
  LPWSTR buffer = nullptr;
  FormatMessageW(FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS,
                nullptr, code, 0, reinterpret_cast<LPWSTR>(&buffer), 0, nullptr);
  std::wstring message = buffer ? buffer : L"Windows 操作失败";
  LocalFree(buffer);
  while (!message.empty() && (message.back() == L'\r' || message.back() == L'\n')) message.pop_back();
  return message + L"（" + std::to_wstring(code) + L"）";
}

std::wstring QuoteArgument(const std::wstring& value) {
  std::wstring result = L"\"";
  size_t slashes = 0;
  for (wchar_t ch : value) {
    if (ch == L'\\') { ++slashes; continue; }
    if (ch == L'\"') result.append(slashes * 2 + 1, L'\\');
    else result.append(slashes, L'\\');
    slashes = 0;
    result += ch;
  }
  result.append(slashes * 2, L'\\');
  return result + L'\"';
}

std::wstring DefaultDirectory(bool allUsers) {
  const auto registered = RegisteredDirectory(allUsers);
  if (!registered.empty()) return registered;
  return (fs::path(KnownFolder(allUsers ? FOLDERID_ProgramFiles : FOLDERID_UserProgramFiles)) / kProductDirectory).wstring();
}

std::wstring ValidateDirectory(const std::wstring& value) {
  if (value.empty()) throw std::runtime_error("请选择安装位置。");
  for (wchar_t ch : value)
    if (ch < 32 || ch == L'\"' || ch == L'<' || ch == L'>' || ch == L'|' || ch == L'*' || ch == L'?')
      throw std::runtime_error("安装路径包含无效字符。");
  if (value.size() > 230) throw std::runtime_error("安装路径太长，请选择较短的目录。");
  if (value.size() < 3 || value[1] != L':' || (value[2] != L'\\' && value[2] != L'/') ||
      !iswalpha(value[0]) || value.find(L':', 2) != std::wstring::npos)
    throw std::runtime_error("请选择本机磁盘上的绝对路径，例如 D:\\Octopus。");
  const fs::path normalized = fs::path(value).lexically_normal();
  if (SamePath(normalized, normalized.root_path()))
    throw std::runtime_error("请选择应用专用文件夹，不能直接安装到磁盘根目录。");
  for (const auto& part : normalized) {
    const auto component = part.wstring();
    if (!component.empty() && (component.back() == L'.' || component.back() == L' '))
      throw std::runtime_error("目录名称不能以空格或句点结尾。");
  }
  for (const KNOWNFOLDERID* id : {&FOLDERID_Windows, &FOLDERID_System, &FOLDERID_Profile,
       &FOLDERID_Desktop, &FOLDERID_Documents, &FOLDERID_ProgramFiles, &FOLDERID_LocalAppData}) {
    const auto protectedPath = KnownFolder(*id);
    if (SamePath(normalized, protectedPath)) throw std::runtime_error("请选择应用专用文件夹，不能覆盖系统或个人目录。");
  }
  const auto data = fs::path(KnownFolder(FOLDERID_Profile)) / L".octopus";
  const auto target = Lower(normalized.wstring());
  if (target == Lower(data.wstring()) || target.starts_with(Lower(data.wstring()) + L"\\"))
    throw std::runtime_error("安装位置不能放在 Octopus 数据目录内。");
  std::error_code error;
  if (fs::exists(normalized, error)) {
    if (!fs::is_directory(normalized, error)) throw std::runtime_error("安装位置已经被同名文件占用。");
    if (!fs::is_empty(normalized, error) &&
        !(fs::exists(normalized / kAppExecutable) && fs::exists(normalized / kUninstallerExecutable)))
      throw std::runtime_error("这个目录包含其他文件，请选择空目录或现有的 Octopus 安装目录。");
  }
  if (error) throw std::runtime_error("无法访问安装位置，请检查路径和权限。");
  return normalized.wstring();
}

std::wstring InstallerArguments(const InstallOptions& options, const std::wstring& stageLog) {
  std::wstring arguments = options.allUsers ? L"/S /allusers" : L"/S /currentuser";
  if (!options.desktopShortcut) arguments += L" /no-desktop-shortcut";
  arguments += L" /OCTOPUS_LOG=" + QuoteArgument(stageLog);
  // electron-builder re-reads /D with StdUtils, so keep the whole token quoted.
  // This override must still be the final argument (including spaces/Unicode).
  return arguments + L" " + QuoteArgument(L"/D=" + options.directory);
}

bool AppIsRunning() {
  Handle processes(CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0));
  if (!processes.valid()) return false;
  PROCESSENTRY32W entry{};
  entry.dwSize = sizeof(entry);
  if (Process32FirstW(processes.get(), &entry)) {
    do { if (_wcsicmp(entry.szExeFile, kAppExecutable) == 0) return true; }
    while (Process32NextW(processes.get(), &entry));
  }
  return false;
}

bool HasPayload(HINSTANCE instance) {
  const HRSRC resource = FindResourceW(instance, MAKEINTRESOURCEW(IDR_PAYLOAD), RT_RCDATA);
  return resource && SizeofResource(instance, resource) == kPayloadBytes && kPayloadBytes > 0;
}

void Install(HINSTANCE instance, HWND owner, InstallOptions options, const std::function<void(Update)>& notify) {
  fs::path session, sessionRoot, logPath;
  std::ofstream log;
  const HRESULT initialized = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
  try {
    options.directory = ValidateDirectory(options.directory);
    const fs::path setupRoot = fs::path(KnownFolder(FOLDERID_LocalAppData)) / L"Octopus" / L"Setup";
    sessionRoot = setupRoot / L"Sessions";
    const auto logs = setupRoot / L"Logs";
    fs::create_directories(logs);
    logPath = logs / (L"setup-" + NewGuid() + L".log");
    log.open(logPath, std::ios::binary);
    if (!log) throw std::runtime_error("无法创建安装日志。");
    log << "Octopus Setup " << Utf8(kVersion) << "\nDirectory: " << Utf8(options.directory)
        << "\nScope: " << (options.allUsers ? "all-users" : "current-user") << '\n' << std::flush;
    notify({Phase::Preparing, L"正在准备安装文件", logPath.wstring(), 0.0f});
    if (!HasPayload(instance)) throw std::runtime_error("安装包缺少文件，请重新下载安装程序。");
    session = PrivateSession(sessionRoot);
    const auto payloadPath = session / L"payload.exe";
    const auto stagesPath = session / L"stages.log";
    const HRSRC resource = FindResourceW(instance, MAKEINTRESOURCEW(IDR_PAYLOAD), RT_RCDATA);
    const HGLOBAL loaded = LoadResource(instance, resource);
    const auto data = static_cast<const BYTE*>(LockResource(loaded));
    if (!data) throw std::runtime_error("无法读取内置安装包。");
    Sha256 digest;
    {
      Handle file(CreateFileW(payloadPath.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr));
      if (!file.valid()) throw std::runtime_error(Utf8(WindowsError(GetLastError())));
      for (DWORD offset = 0; offset < kPayloadBytes;) {
        const DWORD count = std::min<DWORD>(256 * 1024, kPayloadBytes - offset);
        DWORD written = 0;
        if (!WriteFile(file.get(), data + offset, count, &written, nullptr) || written != count)
          throw std::runtime_error("无法释放安装文件，请检查磁盘空间。");
        digest.Add(data + offset, count);
        offset += count;
        notify({Phase::Preparing, L"正在准备安装文件", logPath.wstring(), static_cast<float>(offset) / kPayloadBytes});
      }
      FlushFileBuffers(file.get());
    }
    if (digest.Finish() != kPayloadSha256) throw std::runtime_error("安装文件校验失败，请重新下载安装包。");
    log << "Payload verified\n" << std::flush;
    notify({Phase::Installing, options.allUsers ? L"正在请求 Windows 授权并安装" : L"正在安装 Octopus，请稍候", logPath.wstring()});
    const auto arguments = InstallerArguments(options, stagesPath.wstring());
    log << "Starting installation engine\n" << std::flush;
    SHELLEXECUTEINFOW execute{};
    execute.cbSize = sizeof(execute);
    execute.fMask = SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC | SEE_MASK_FLAG_NO_UI;
    execute.hwnd = owner;
    execute.lpVerb = options.allUsers ? L"runas" : L"open";
    execute.lpFile = payloadPath.c_str();
    execute.lpParameters = arguments.c_str();
    execute.lpDirectory = session.c_str();
    execute.nShow = SW_HIDE;
    if (!ShellExecuteExW(&execute)) {
      const DWORD error = GetLastError();
      if (error == ERROR_CANCELLED) throw std::runtime_error("已取消 Windows 授权，尚未开始安装。可重试或改为仅当前用户。");
      throw std::runtime_error(Utf8(WindowsError(error)));
    }
    Handle child(execute.hProcess);
    if (!child.valid()) throw std::runtime_error("无法跟踪安装进程，请查看系统安装状态后重试。");
    while (WaitForSingleObject(child.get(), 250) == WAIT_TIMEOUT) { /* Keep the UI responsive. */ }
    DWORD exitCode = 0;
    if (!GetExitCodeProcess(child.get(), &exitCode)) throw std::runtime_error("无法读取安装结果。");
    const auto stages = ReadFile(stagesPath);
    log << stages << "\nExit code: " << exitCode << " (0x" << std::hex << exitCode << std::dec << ")\n" << std::flush;
    if (exitCode == 0xc0000005u) throw std::runtime_error("Windows 安装引擎发生内存访问异常。请保留日志并反馈，或重新下载最新安装包。");
    if (exitCode != 0) throw std::runtime_error("安装没有完成，退出码 " + std::to_string(exitCode) + "。请查看日志或重试。");
    if (stages.find("complete\r\n") == std::string::npos)
      throw std::runtime_error("安装程序已退出，但未确认完成。请检查目录权限和正在运行的 Octopus。");
    const fs::path executable = fs::path(options.directory) / kAppExecutable;
    if (!fs::exists(executable) || FileHash(executable) != kInstalledExeSha256)
      throw std::runtime_error("安装后的程序校验失败，可能被占用或拦截。请查看日志后重试。");
    log << "Installed executable verified\nComplete\n" << std::flush;
    CleanSession(session, sessionRoot);
    notify({Phase::Complete, L"安装完成，可以开始使用了", logPath.wstring(), 1.0f});
  } catch (const std::exception& error) {
    if (log) log << "Error: " << error.what() << '\n' << std::flush;
    CleanSession(session, sessionRoot);
    notify({Phase::Failed, Wide(error.what()), logPath.wstring()});
  }
  if (SUCCEEDED(initialized)) CoUninitialize();
}

bool SelfTest(HINSTANCE instance, const fs::path& report) {
  bool valid = true;
  std::ofstream output(report, std::ios::binary);
  auto check = [&](const char* name, bool passed) { output << (passed ? "PASS " : "FAIL ") << name << '\n'; valid &= passed; };
  check("quote spaces", QuoteArgument(L"C:\\path with spaces\\file.exe") == L"\"C:\\path with spaces\\file.exe\"");
  check("quote trailing slash", QuoteArgument(L"C:\\path\\") == L"\"C:\\path\\\\\"");
  for (const auto& bad : {L"C:\\", L"D:\\", L"relative\\app", L"C:\\bad\"path", L"C:\\..\\", L"\\\\server\\app"}) {
    bool rejected = false;
    try { ValidateDirectory(bad); } catch (...) { rejected = true; }
    check("reject unsafe directory", rejected);
  }
  const InstallOptions options{L"D:\\测试 安装\\Octopus", false, false};
  const auto args = InstallerArguments(options, L"C:\\测试 日志\\stages.log");
  check("NSIS directory is a complete final token", args.ends_with(L"\"/D=D:\\测试 安装\\Octopus\""));
  check("shortcut preference", args.find(L"/no-desktop-shortcut") != std::wstring::npos);
  try { check("default user directory", !DefaultDirectory(false).empty()); } catch (...) { check("default user directory", false); }
  check("payload metadata", kPayloadBytes == 0 || HasPayload(instance));
  if (kPayloadBytes && HasPayload(instance)) {
    const auto resource = FindResourceW(instance, MAKEINTRESOURCEW(IDR_PAYLOAD), RT_RCDATA);
    const auto data = static_cast<const BYTE*>(LockResource(LoadResource(instance, resource)));
    if (data) {
      Sha256 digest;
      digest.Add(data, kPayloadBytes);
      check("embedded payload SHA-256", digest.Finish() == kPayloadSha256);
    } else check("embedded payload SHA-256", false);
  }
  output << "Version: " << Utf8(kVersion) << '\n';
  return valid && output.good();
}
}  // namespace setup
