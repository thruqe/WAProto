package extractor

import (
	"testing"
)

func TestExtractor_ProcessSource_OldFormat(t *testing.T) {
	sampleJS := `
	__d("WAAdv", [], (function(a, b, c, d, e, f) {
		const ADVEncryptionType = {
			E2EE: 0,
			HOSTED: 1,
			NON_E2EE: 2
		};
		f.ADVEncryptionType = ADVEncryptionType;

		f.ADVKeyIndexListSpec = {
			rawId: [1, b.TYPES.UINT32],
			timestamp: [2, b.TYPES.UINT64],
			currentIndex: [3, b.TYPES.UINT32],
			validIndexes: [4, b.FLAGS.PACKED | b.TYPES.UINT32 | b.FLAGS.REPEATED],
			accountType: [5, b.TYPES.ENUM, ADVEncryptionType],
			__oneofs__: {
				choice: ["rawId", "timestamp"]
			}
		};
	}));
	`

	ext := NewExtractor("2.3000.1000")
	if err := ext.ProcessSource(sampleJS); err != nil {
		t.Fatalf("ProcessSource failed: %v", err)
	}

	mod := ext.Modules["WAAdv"]
	if mod == nil {
		t.Fatalf("expected WAAdv module, got nil")
	}

	if len(mod.Enums) != 1 {
		t.Errorf("expected 1 enum, got %d", len(mod.Enums))
	}
	if len(mod.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(mod.Messages))
	}

	msg := mod.Messages[0]
	if msg.Name != "ADVKeyIndexList" {
		t.Errorf("expected message name ADVKeyIndexList, got %s", msg.Name)
	}
	if len(msg.Oneofs) != 1 {
		t.Errorf("expected 1 oneof, got %d", len(msg.Oneofs))
	}
	if msg.Oneofs[0].Name != "choice" || len(msg.Oneofs[0].Fields) != 2 {
		t.Errorf("expected oneof 'choice' with 2 fields, got %+v", msg.Oneofs[0])
	}
}

// TestExtractor_ProcessSource_NewFormat tests the new WhatsApp bundle format
// where specs use .internalSpec on intermediate container objects and enums
// use the $InternalEnum() call pattern.
func TestExtractor_ProcessSource_NewFormat(t *testing.T) {
	// Mirrors a real new-format bundle like WAWebProtobufsEphemeral.pb
	sampleJS := `
	__d("WAWebProtobufsEphemeral.pb", ["WAProtoConst"], (function(t, n, r, o, a, i, l) {
		var e = {};
		e.name = "EphemeralSetting";
		e.internalSpec = {
			duration: [1, o("WAProtoConst").TYPES.SFIXED32],
			timestamp: [2, o("WAProtoConst").TYPES.SFIXED64]
		};
		l.EphemeralSettingSpec = e;
	}));
	`

	ext := NewExtractor("2.3000.1000")
	if err := ext.ProcessSource(sampleJS); err != nil {
		t.Fatalf("ProcessSource failed: %v", err)
	}

	mod := ext.Modules["WAWebProtobufsEphemeral.pb"]
	if mod == nil {
		t.Fatalf("expected WAWebProtobufsEphemeral.pb module, got nil")
	}

	if len(mod.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(mod.Messages))
	}

	msg := mod.Messages[0]
	if msg.Name != "EphemeralSetting" {
		t.Errorf("expected message name EphemeralSetting, got %s", msg.Name)
	}
	if len(msg.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(msg.Fields))
	}
}

// TestExtractor_ProcessSource_NewFormatWithInternalEnum tests $InternalEnum enum extraction.
func TestExtractor_ProcessSource_NewFormatWithInternalEnum(t *testing.T) {
	sampleJS := `
	__d("WAWebProtobufsHistorySync.pb", ["$InternalEnum", "WAProtoConst"], (function(t, n, r, o, a, i, l) {
		var e, s;
		var u = (s = n("$InternalEnum"))({IN_WAITLIST: 0, AI_AVAILABLE: 1});
		var c = s({INITIAL_BOOTSTRAP: 0, INITIAL_STATUS_V3: 1, FULL: 2, RECENT: 3});
		var h = {};
		h.name = "HistorySync";
		h.internalSpec = {
			syncType: [1, (e = o("WAProtoConst")).TYPES.ENUM, c],
			progress: [6, e.TYPES.UINT32]
		};
		l.HistorySyncSpec = h;
		l.HistorySyncSyncType = c;
	}));
	`

	ext := NewExtractor("2.3000.1000")
	if err := ext.ProcessSource(sampleJS); err != nil {
		t.Fatalf("ProcessSource failed: %v", err)
	}

	mod := ext.Modules["WAWebProtobufsHistorySync.pb"]
	if mod == nil {
		t.Fatalf("expected module, got nil")
	}

	if len(mod.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(mod.Messages))
	}
	if msg := mod.Messages[0]; msg.Name != "HistorySync" {
		t.Errorf("expected message name HistorySync, got %s", msg.Name)
	}
	if len(mod.Messages[0].Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(mod.Messages[0].Fields))
	}

	// At least one enum should be exported (HistorySyncSyncType)
	if len(mod.Enums) < 1 {
		t.Errorf("expected at least 1 enum, got %d", len(mod.Enums))
	}
}

// TestExtractor_ProcessSource_NewFormatMultipleMessages tests multiple spec containers in one module.
func TestExtractor_ProcessSource_NewFormatMultipleMessages(t *testing.T) {
	sampleJS := `
	__d("WASignalProto.pb", ["WAProtoConst"], (function(t, n, r, o, a, i, l) {
		var e;
		var s = {}, u = {};
		s.name = "SessionStructure";
		s.internalSpec = {
			sessionVersion: [1, (e = o("WAProtoConst")).TYPES.UINT32],
			rootKey: [4, e.TYPES.BYTES],
			senderChain: [6, e.TYPES.MESSAGE, u]
		};
		u.name = "SessionStructure$Chain";
		u.internalSpec = {
			senderRatchetKey: [1, e.TYPES.BYTES],
			chainIndex: [2, e.TYPES.UINT32]
		};
		l.SessionStructureSpec = s;
		l.SessionStructure$ChainSpec = u;
	}));
	`

	ext := NewExtractor("2.3000.1000")
	if err := ext.ProcessSource(sampleJS); err != nil {
		t.Fatalf("ProcessSource failed: %v", err)
	}

	mod := ext.Modules["WASignalProto.pb"]
	if mod == nil {
		t.Fatalf("expected module, got nil")
	}

	if len(mod.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d: %v", len(mod.Messages), func() []string {
			names := make([]string, len(mod.Messages))
			for i, m := range mod.Messages {
				names[i] = m.Name
			}
			return names
		}())
	}
}

func TestExtractor_RejectInvalidEnums(t *testing.T) {
	sampleJS := `
	__d("InvalidEnums", [], (function(t, n, r, o, a, i, l) {
		l.NumericKeys = { 8248: 16, 8300: 11 };
		l.MimeKeys = { "application/json": 0, "text/plain": 1 };
		l.OutOfRangeInt = { ARTICLE_ID: 2107457129437300 };
		l.ValidEnum = { UNKNOWN: 0, FIRST: 1 };
	}));
	`

	ext := NewExtractor("2.3000.1000")
	if err := ext.ProcessSource(sampleJS); err != nil {
		t.Fatalf("ProcessSource failed: %v", err)
	}

	mod := ext.Modules["InvalidEnums"]
	if mod == nil {
		t.Fatalf("expected module, got nil")
	}

	if len(mod.Enums) != 1 {
		t.Fatalf("expected only 1 valid enum, got %d", len(mod.Enums))
	}
	if mod.Enums[0].Name != "ValidEnum" {
		t.Errorf("expected ValidEnum, got %s", mod.Enums[0].Name)
	}
}

func TestExtractor_TypeResolution(t *testing.T) {
	sampleJS := `
	__d("TypeTest.pb", ["WAProtoConst"], (function(t, n, r, o, a, i, l) {
		var e = o("WAProtoConst");
		var m = {};
		m.name = "Parent";
		m.internalSpec = {
			childRef: [1, e.TYPES.MESSAGE, "Parent$ChildSpec"],
			stringMap: [2, e.TYPES.MAP, [e.TYPES.STRING, e.TYPES.UINT32]]
		};
		l.ParentSpec = m;
	}));
	`

	ext := NewExtractor("2.3000.1000")
	if err := ext.ProcessSource(sampleJS); err != nil {
		t.Fatalf("ProcessSource failed: %v", err)
	}

	mod := ext.Modules["TypeTest.pb"]
	if mod == nil || len(mod.Messages) != 1 {
		t.Fatalf("expected 1 message, got %+v", mod)
	}

	msg := mod.Messages[0]
	if len(msg.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(msg.Fields))
	}

	if msg.Fields[0].Type != "Parent.Child" {
		t.Errorf("expected field childRef to have type Parent.Child, got %s", msg.Fields[0].Type)
	}
	if msg.Fields[1].Type != "map<string, uint32>" {
		t.Errorf("expected field stringMap to have type map<string, uint32>, got %s", msg.Fields[1].Type)
	}
}
