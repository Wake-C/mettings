// Continuous, local speech-gated capture for Mettings.
// stdin: mono 16 kHz float32 little-endian PCM.
// stdout: JSONL activity and immutable, finalized PCM16 WAV clip events.
#include "include/c-api.h"

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <deque>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>
#include <sys/stat.h>

namespace {
constexpr int kSampleRate = 16000;
constexpr int kWindow = 512;
constexpr int64_t kPreRoll = 2 * kSampleRate;
constexpr int64_t kTail = kSampleRate;
constexpr int64_t kMaxClip = 30 * kSampleRate;
// Flush before the 30 s file bound, leaving space for delayed detection/pre-roll.
constexpr int64_t kForceSpeech = 27 * kSampleRate;
constexpr int64_t kHistory = 36 * kSampleRate;

std::string jsonString(const std::string &value) {
  std::ostringstream out;
  out << '"';
  for (unsigned char ch : value) {
    switch (ch) {
      case '"': out << "\\\""; break;
      case '\\': out << "\\\\"; break;
      case '\n': out << "\\n"; break;
      case '\r': out << "\\r"; break;
      case '\t': out << "\\t"; break;
      default:
        if (ch < 32) out << "\\u" << std::hex << std::setw(4) << std::setfill('0') << unsigned(ch) << std::dec;
        else out << ch;
    }
  }
  out << '"';
  return out.str();
}

void le16(std::ostream &out, uint16_t n) {
  const char bytes[] = {char(n), char(n >> 8)};
  out.write(bytes, sizeof(bytes));
}
void le32(std::ostream &out, uint32_t n) {
  const char bytes[] = {char(n), char(n >> 8), char(n >> 16), char(n >> 24)};
  out.write(bytes, sizeof(bytes));
}

class Capture {
 public:
  Capture(const SherpaOnnxVoiceActivityDetector *vad, std::filesystem::path spool)
      : vad_(vad), spool_(std::filesystem::absolute(std::move(spool))) {
    std::filesystem::create_directories(spool_);
  }

  void accept(const float *samples, int n) {
    history_.insert(history_.end(), samples, samples + n);
    total_ += n;
    while (history_.size() > kHistory) { history_.pop_front(); ++head_; }

    // Only the final frame can be short. Zero padding lets VAD process it;
    // total_ remains the real source length and bounds every retained range.
    if (n < kWindow) {
      float padded[kWindow]{};
      std::copy(samples, samples + n, padded);
      SherpaOnnxVoiceActivityDetectorAcceptWaveform(vad_, padded, kWindow);
    } else {
      SherpaOnnxVoiceActivityDetectorAcceptWaveform(vad_, samples, n);
    }

    bool detected = SherpaOnnxVoiceActivityDetectorDetected(vad_) != 0;
    if (detected && activeSince_ < 0) activeSince_ = total_;
    if (detected && total_ - activeSince_ >= kForceSpeech) {
      SherpaOnnxVoiceActivityDetectorFlush(vad_);
      activeSince_ = -1;
    } else if (!detected) {
      activeSince_ = -1;
    }
    drain();
    if (total_ >= nextActivity_) {
      activity(detected);
      nextActivity_ = (total_ / kSampleRate + 1) * kSampleRate;
    }
  }

  void finish() {
    SherpaOnnxVoiceActivityDetectorFlush(vad_);
    drain();
    activity(false);
  }

 private:
  void activity(bool speech) {
    std::cout << "{\"type\":\"activity\",\"at_ms\":" << total_ * 1000 / kSampleRate
              << ",\"speech\":" << (speech ? "true" : "false") << "}\n" << std::flush;
    if (!std::cout) throw std::runtime_error("Cannot report capture activity");
  }

  void drain() {
    while (!SherpaOnnxVoiceActivityDetectorEmpty(vad_)) {
      const auto *segment = SherpaOnnxVoiceActivityDetectorFront(vad_);
      if (!segment || segment->start < 0 || segment->n <= 0) {
        if (segment) SherpaOnnxDestroySpeechSegment(segment);
        SherpaOnnxVoiceActivityDetectorPop(vad_);
        continue;
      }
      const int64_t start = std::max({int64_t(0), segment->start - kPreRoll, emittedEnd_});
      const int64_t end = std::min(total_, segment->start + segment->n + kTail);
      SherpaOnnxDestroySpeechSegment(segment);
      SherpaOnnxVoiceActivityDetectorPop(vad_);
      if (start < head_) throw std::runtime_error("Speech exceeded the bounded capture history");
      for (int64_t offset = start; offset < end; offset += kMaxClip) {
        const int64_t stop = std::min(end, offset + kMaxClip);
        writeClip(offset, stop);
      }
    }
  }

  void writeClip(int64_t start, int64_t end) {
    if (end <= start) return;
    std::filesystem::path target, temporary;
    do {
      std::ostringstream name;
      name << "clip-" << std::setw(6) << std::setfill('0') << ++sequence_ << ".wav";
      target = spool_ / name.str();
      temporary = target.string() + ".part";
    } while (std::filesystem::exists(target) || std::filesystem::exists(temporary));

    try {
      std::ofstream out(temporary, std::ios::binary | std::ios::out);
      if (!out) throw std::runtime_error("Cannot create speech clip");
      const auto bytes = uint32_t((end - start) * 2);
      out.write("RIFF", 4); le32(out, 36 + bytes); out.write("WAVEfmt ", 8);
      le32(out, 16); le16(out, 1); le16(out, 1); le32(out, kSampleRate);
      le32(out, kSampleRate * 2); le16(out, 2); le16(out, 16);
      out.write("data", 4); le32(out, bytes);
      std::vector<char> pcm(bytes);
      for (int64_t sample = start; sample < end; ++sample) {
        float value = history_[size_t(sample - head_)];
        if (!std::isfinite(value)) value = 0;
        const auto quantized = int16_t(std::clamp(std::lround(value * 32768.0f), -32768L, 32767L));
        const size_t index = size_t(sample - start) * 2;
        pcm[index] = char(quantized); pcm[index + 1] = char(uint16_t(quantized) >> 8);
      }
      out.write(pcm.data(), std::streamsize(pcm.size()));
      out.close();
      if (!out) throw std::runtime_error("Cannot finish speech clip");
      std::filesystem::rename(temporary, target);
    } catch (...) {
      std::error_code ignored;
      std::filesystem::remove(temporary, ignored);
      throw;
    }
    emittedEnd_ = end;
    std::cout << "{\"type\":\"clip\",\"path\":" << jsonString(target.string())
              << ",\"start_ms\":" << start * 1000 / kSampleRate
              << ",\"end_ms\":" << end * 1000 / kSampleRate
              << ",\"start_sample\":" << start << ",\"end_sample\":" << end
              << ",\"sample_rate\":" << kSampleRate << "}\n" << std::flush;
    if (!std::cout) throw std::runtime_error("Cannot report saved speech clip");
  }

  const SherpaOnnxVoiceActivityDetector *vad_;
  std::filesystem::path spool_;
  std::deque<float> history_;
  int64_t total_ = 0, head_ = 0, emittedEnd_ = 0, activeSince_ = -1;
  int64_t nextActivity_ = kSampleRate;
  uint64_t sequence_ = 0;
};
}  // namespace

int main(int argc, char **argv) {
  if (argc == 2 && std::string(argv[1]) == "--version") {
    std::cout << "mettings-desk 1 (Silero VAD / sherpa-onnx 2.0.1)\n";
    return 0;
  }
  if (argc != 3) {
    std::cerr << "Usage: mettings-desk <silero-vad-model> <spool-directory>\n";
    return 2;
  }
  umask(0077);
  SherpaOnnxVadModelConfig config{};
  config.silero_vad.model = argv[1];
  config.silero_vad.threshold = .30f;
  config.silero_vad.min_silence_duration = 1.2f;
  config.silero_vad.min_speech_duration = .20f;
  config.silero_vad.max_speech_duration = 30.0f;
  config.silero_vad.window_size = kWindow;
  config.sample_rate = kSampleRate;
  config.num_threads = 1;
  config.provider = "cpu";
  const auto *vad = SherpaOnnxCreateVoiceActivityDetector(&config, 36.0f);
  if (!vad) { std::cerr << "Cannot load the local speech detector\n"; return 1; }

  int status = 0;
  try {
    Capture capture(vad, argv[2]);
    float samples[kWindow];
    while (std::cin) {
      std::cin.read(reinterpret_cast<char *>(samples), sizeof(samples));
      const auto bytes = std::cin.gcount();
      if (bytes % sizeof(float) != 0) throw std::runtime_error("Incomplete float32 PCM input");
      if (bytes) capture.accept(samples, int(bytes / sizeof(float)));
    }
    if (!std::cin.eof()) throw std::runtime_error("Cannot read microphone PCM stream");
    capture.finish();
  } catch (const std::exception &error) {
    // Operational errors only. Never log captured audio or transcript content.
    std::cerr << error.what() << '\n';
    status = 1;
  }
  SherpaOnnxDestroyVoiceActivityDetector(vad);
  return status;
}
