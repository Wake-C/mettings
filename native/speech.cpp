#include "include/c-api.h"
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <iostream>
#include <fstream>
#include <string>

// stdout is a single JSON result. Diagnostic output from sherpa stays on stderr.
static std::string quote(const char *value) {
  std::string out = "\"";
  for (const unsigned char *p = reinterpret_cast<const unsigned char *>(value ? value : ""); *p; ++p) {
    switch (*p) {
      case '"': out += "\\\""; break;
      case '\\': out += "\\\\"; break;
      case '\n': out += "\\n"; break;
      case '\r': out += "\\r"; break;
      case '\t': out += "\\t"; break;
      default: if (*p < 32) { char b[7]; snprintf(b, sizeof(b), "\\u%04x", *p); out += b; } else out += char(*p);
    }
  }
  return out + "\"";
}
static int fail(const char *message) { std::cerr << message << '\n'; return 1; }
static int embedding(const char *model, const SherpaOnnxWave *wave) {
  SherpaOnnxSpeakerEmbeddingExtractorConfig c{}; c.model=model; c.num_threads=2; c.provider="cpu";
  auto ex=SherpaOnnxCreateSpeakerEmbeddingExtractor(&c); if(!ex) return fail("Cannot load speaker model");
  auto stream=SherpaOnnxSpeakerEmbeddingExtractorCreateStream(ex);
  SherpaOnnxOnlineStreamAcceptWaveform(stream,wave->sample_rate,wave->samples,wave->num_samples);
  SherpaOnnxOnlineStreamInputFinished(stream);
  if(!SherpaOnnxSpeakerEmbeddingExtractorIsReady(ex,stream)) { SherpaOnnxDestroyOnlineStream(stream); SherpaOnnxDestroySpeakerEmbeddingExtractor(ex); return fail("The voice clip is too short"); }
  auto vector=SherpaOnnxSpeakerEmbeddingExtractorComputeEmbedding(ex,stream);
  if(!vector) { SherpaOnnxDestroyOnlineStream(stream); SherpaOnnxDestroySpeakerEmbeddingExtractor(ex); return fail("No speaker embedding returned"); }
  int dim=SherpaOnnxSpeakerEmbeddingExtractorDim(ex); double norm=0; for(int i=0;i<dim;i++)norm+=vector[i]*vector[i];norm=std::sqrt(norm);
  std::cout << "[";for(int i=0;i<dim;i++){if(i)std::cout<<",";std::cout<<(norm>0?vector[i]/norm:0);}std::cout<<"]\n";
  SherpaOnnxSpeakerEmbeddingExtractorDestroyEmbedding(vector);SherpaOnnxDestroyOnlineStream(stream);SherpaOnnxDestroySpeakerEmbeddingExtractor(ex);return 0;
}
static int transcribe(const char *kind,const char *directory,const char *speaker,const char *segmentation,const char *hotwordsFile,const SherpaOnnxWave *wave) {
  SherpaOnnxOfflineSpeakerDiarizationConfig dc{};
  dc.segmentation.pyannote.model=segmentation;dc.segmentation.num_threads=2;dc.segmentation.provider="cpu";
  dc.embedding.model=speaker;dc.embedding.num_threads=2;dc.embedding.provider="cpu";
  dc.clustering.num_clusters=-1;dc.clustering.threshold=.5;dc.min_duration_on=.3;dc.min_duration_off=.5;
  auto diarizer=SherpaOnnxCreateOfflineSpeakerDiarization(&dc);if(!diarizer)return fail("Cannot load diarization models");
  if(SherpaOnnxOfflineSpeakerDiarizationGetSampleRate(diarizer)!=wave->sample_rate){SherpaOnnxDestroyOfflineSpeakerDiarization(diarizer);return fail("Expected mono 16 kHz WAV");}
  auto segments=SherpaOnnxOfflineSpeakerDiarizationProcess(diarizer,wave->samples,wave->num_samples);
  if(!segments){SherpaOnnxDestroyOfflineSpeakerDiarization(diarizer);return fail("Speaker segmentation failed");}
  auto sorted=SherpaOnnxOfflineSpeakerDiarizationResultSortByStartTime(segments);
  int count=SherpaOnnxOfflineSpeakerDiarizationResultGetNumSegments(segments);
  // Speaker separation is optional. It must not suppress decodable short speech.
  bool fallback = count == 0;
  bool nonzero = std::any_of(wave->samples,wave->samples+wave->num_samples,[](float v){return v!=0.0f;});
  if(fallback && !nonzero){
    std::cout<<"{\"result\":{\"utterances\":[]}}\n";
    SherpaOnnxOfflineSpeakerDiarizationDestroySegment(sorted);SherpaOnnxOfflineSpeakerDiarizationDestroyResult(segments);SherpaOnnxDestroyOfflineSpeakerDiarization(diarizer);return 0;
  }
  SherpaOnnxOfflineRecognizerConfig rc{};rc.feat_config.sample_rate=16000;rc.feat_config.feature_dim=80;
  std::string root(directory), model=root+"/model.int8.onnx", tokens=root+"/tokens.txt";
  std::string conv=root+"/conv_frontend.onnx", encoder=root+"/encoder.int8.onnx", decoder=root+"/decoder.int8.onnx", tokenizer=root+"/tokenizer";
  std::ifstream hints(hotwordsFile); std::string hotwords,line;
  while(std::getline(hints,line)){if(line.empty()||line[0]=='#')continue;if(!hotwords.empty())hotwords+=",";hotwords+=line;}
  if(std::string(kind)=="qwen3" || std::string(kind)=="qwen3-1.7b"){
    auto &q=rc.model_config.qwen3_asr;q.conv_frontend=conv.c_str();q.encoder=encoder.c_str();q.decoder=decoder.c_str();q.tokenizer=tokenizer.c_str();
    q.max_total_len=std::string(kind)=="qwen3-1.7b"?512:1024;q.max_new_tokens=std::string(kind)=="qwen3-1.7b"?256:512;q.temperature=1e-6f;q.top_p=.8f;q.seed=42;q.hotwords=hotwords.c_str();
  }else if(std::string(kind)=="firered2"){
    rc.model_config.fire_red_asr.encoder=encoder.c_str();rc.model_config.fire_red_asr.decoder=decoder.c_str();rc.model_config.tokens=tokens.c_str();
  }else if(std::string(kind)=="sensevoice"){
    rc.model_config.sense_voice.model=model.c_str();rc.model_config.sense_voice.language="auto";rc.model_config.sense_voice.use_itn=1;rc.model_config.tokens=tokens.c_str();
  }else{return fail("Unknown local ASR model");}
  rc.model_config.num_threads=4;rc.model_config.provider="cpu";rc.decoding_method="greedy_search";
  auto recognizer=SherpaOnnxCreateOfflineRecognizer(&rc);if(!recognizer){SherpaOnnxOfflineSpeakerDiarizationDestroySegment(sorted);SherpaOnnxOfflineSpeakerDiarizationDestroyResult(segments);SherpaOnnxDestroyOfflineSpeakerDiarization(diarizer);return fail("Cannot load transcription model");}
  bool first=true;std::cout<<"{\"result\":{\"utterances\":[";
  for(int i=0;i<(fallback?1:count);i++){
    int begin=fallback?0:std::max(0,int((sorted[i].start-.25f)*16000));int end=fallback?wave->num_samples:std::min(wave->num_samples,int((sorted[i].end+.1f)*16000));
    std::string speakerName=fallback?"":std::to_string(sorted[i].speaker+1);
    // Bound recognition windows while retaining the diarizer's speaker identity.
    int window=(std::string(kind)=="qwen3-1.7b"?20:25)*16000;
    for(int offset=begin;offset<end;offset+=window){
      int n=std::min(window,end-offset);if(n<1600)continue;
      auto stream=SherpaOnnxCreateOfflineStream(recognizer);
      SherpaOnnxAcceptWaveformOffline(stream,16000,wave->samples+offset,n);SherpaOnnxDecodeOfflineStream(recognizer,stream);
      auto result=SherpaOnnxGetOfflineStreamResult(stream);
      if(result && result->text && *result->text){if(!first)std::cout<<",";first=false;std::cout<<"{\"start_time\":"<<offset/16<<",\"end_time\":"<<(offset+n)/16<<",\"text\":"<<quote(result->text)<<",\"additions\":{\"speaker\":\""<<speakerName<<"\"}}";}
      if(result)SherpaOnnxDestroyOfflineRecognizerResult(result);SherpaOnnxDestroyOfflineStream(stream);
    }
  }
  std::cout<<"]},\"diarization_fallback\":"<<(fallback?"true":"false")<<"}\n";
  SherpaOnnxDestroyOfflineRecognizer(recognizer);SherpaOnnxOfflineSpeakerDiarizationDestroySegment(sorted);SherpaOnnxOfflineSpeakerDiarizationDestroyResult(segments);SherpaOnnxDestroyOfflineSpeakerDiarization(diarizer);return 0;
}
int main(int argc,char **argv){
  if(argc==2 && std::string(argv[1])=="--version"){std::cout<<"mettings-speech 1 (sherpa-onnx 2.0.1)\n";return 0;}
  if(argc!=4 && argc!=8)return fail("Usage: mettings-speech embed model wav | transcribe kind directory speaker segmentation hotwords-file wav");
  auto wave=SherpaOnnxReadWave(argv[argc-1]);if(!wave)return fail("Could not read the input WAV");
  int result=1;if(std::string(argv[1])=="embed" && argc==4)result=embedding(argv[2],wave);
  else if(std::string(argv[1])=="transcribe" && argc==8)result=transcribe(argv[2],argv[3],argv[4],argv[5],argv[6],wave);
  SherpaOnnxFreeWave(wave);return result;
}
