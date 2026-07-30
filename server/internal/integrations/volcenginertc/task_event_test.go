package volcenginertc

import "testing"

func TestVoiceChatEventEnvelopeVerifiesOfficialSignatureExample(t *testing.T) {
	envelope := VoiceChatEventEnvelope{
		EventType: "RoomCreate",
		EventData: `{"RoomId":"room1","Timestamp":1679383924691}`,
		EventTime: "2023-03-21T15:32:04+08:00",
		EventID:   "123456",
		AppID:     "appId",
		Version:   "2020-12-01",
		Nonce:     "aaBc",
		Signature: "1c7200723842eff514b65fc3f065597432bbb4249e10d33db79b3853d05f3691",
	}

	if !envelope.VerifySignature("1234") {
		t.Fatal("official Volcengine callback signature example did not verify")
	}
	if envelope.VerifySignature("wrong-secret") {
		t.Fatal("callback signature verified with the wrong secret")
	}
}

func TestDecodeVoiceChatTaskEventAcceptsTaskStart(t *testing.T) {
	envelope := VoiceChatEventEnvelope{
		EventType: "VoiceChat",
		EventData: `{"AppId":"rtc-app","BusinessId":"","RoomId":"voice-call-abc","TaskId":"voice-task-abc","UserID":"voice-agent-abc","RoundID":0,"EventTime":1785391200000,"EventType":0,"RunStage":"taskStart"}`,
	}

	event, err := DecodeVoiceChatTaskEvent(envelope)
	if err != nil {
		t.Fatalf("decode VoiceChat task event: %v", err)
	}
	if event.AppID != "rtc-app" ||
		event.RoomID != "voice-call-abc" ||
		event.TaskID != "voice-task-abc" ||
		event.UserID != "voice-agent-abc" ||
		event.RoundID != 0 ||
		event.EventTime != 1785391200000 ||
		event.EventType != VoiceChatTaskStateChanged ||
		event.RunStage != VoiceChatRunStageTaskStart {
		t.Fatalf("event = %#v", event)
	}
}

func TestDecodeVoiceChatTaskEventRejectsErrorWithoutDetails(t *testing.T) {
	envelope := VoiceChatEventEnvelope{
		EventType: "VoiceChat",
		EventData: `{"AppId":"rtc-app","RoomId":"voice-call-abc","TaskId":"voice-task-abc","EventTime":1785391200000,"EventType":1,"RunStage":"preParamCheck"}`,
	}

	if _, err := DecodeVoiceChatTaskEvent(envelope); err == nil {
		t.Fatal("VoiceChat error event without ErrorInfo was accepted")
	}
}
