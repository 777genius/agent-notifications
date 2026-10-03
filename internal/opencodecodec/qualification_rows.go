package opencodecodec

// Row data is separate from algorithm bytes. No semantic IDs are frozen here:
// root must first bind complete source/native evidence to this changed algorithm.
type ImageKey struct {
	GOOS, GOARCH, Entry, SHA256 string
}

type Candidate struct {
	Version, Generation string
}

// These are official image identities, not proof of an executing process.
var candidateImages = [...]struct {
	key       ImageKey
	candidate Candidate
}{
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, Candidate{"1.18.34", "v1"}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, Candidate{"2.0.21", "v2"}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, Candidate{"2.0.21", "v2"}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, Candidate{"2.0.21", "v2"}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, Candidate{"2.0.21", "v2"}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, Candidate{"2.0.21", "v2"}},
	// Exact accepted local-entry SOURCE keys; no Windows TUI inheritance.
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, Candidate{"1.18.34", "v1"}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, Candidate{"1.18.34", "v1"}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, Candidate{"1.18.33", "v1"}},
}

type ClockRow struct {
	OriginalNativeAge                                        string
	ProfileID, CalibrationID, Generation, RawKind            string
	NativeReadBoundNS, ComparisonBoundNS, TranslationBoundNS int64
}

// Reviewed native/source prerequisites are compiled here. Sampled maxima alone
// cannot populate this table. There is no public registration or grant setter.
var qualifiedClockRows = [...]struct {
	key ImageKey
	row ClockRow
}{
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e2de540707a34e6f398dc8c5deab2ad70d1d69487cd643b6b8195c78d1d3c357", "linux-amd64-proc-boottime-v1:e2de540707a34e6f398dc8c5deab2ad70d1d69487cd643b6b8195c78d1d3c357:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:e235c6f88ce85b5f9f58de08f96cad0949dd815f885d7c48e92e1deb39390343", "linux-arm64-linux-proc-boottime-v1:e235c6f88ce85b5f9f58de08f96cad0949dd815f885d7c48e92e1deb39390343:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:6821cbc386ed64be3736e144d2ea723d4ab9d1e391c42e0ae8f8404c28203c61", "linux-arm64-linux-proc-boottime-v1:6821cbc386ed64be3736e144d2ea723d4ab9d1e391c42e0ae8f8404c28203c61:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:0f71174bcf5117aac96dd8d72d65c8fe19400c8427207418ca6dd385f0399eef", "darwin-amd64-darwin-mach-continuous-v1:0f71174bcf5117aac96dd8d72d65c8fe19400c8427207418ca6dd385f0399eef:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:3e2a4e17d4b7cf8ed37f9bdcec2933229299abde38675b3cf26dfeeef6f6d0c4", "darwin-amd64-darwin-mach-continuous-v1:3e2a4e17d4b7cf8ed37f9bdcec2933229299abde38675b3cf26dfeeef6f6d0c4:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:ac25a482239171fb6c2f30ed7a99fde2dcbc5286ce843be85396b3e54c6059c4", "darwin-arm64-darwin-mach-continuous-v1:ac25a482239171fb6c2f30ed7a99fde2dcbc5286ce843be85396b3e54c6059c4:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:cb87b3cf8c7768b2e03b93a82ced47b45ce56cdf7bde79b14875168a80e81560", "darwin-arm64-darwin-mach-continuous-v1:cb87b3cf8c7768b2e03b93a82ced47b45ce56cdf7bde79b14875168a80e81560:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:8c2962978336831dc8b55e8f7d0522fc4d9f30f37d47d099930a08b1d5c7cd46", "windows-amd64-windows-interrupt-precise-v1:8c2962978336831dc8b55e8f7d0522fc4d9f30f37d47d099930a08b1d5c7cd46:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, ClockRow{"bounded", "windows-amd64-windows-interrupt-precise-v1:966f5ed0034c4d3bd8ea5b4e1d37733f617d96363e154801722cc7e996fa6dd0", "windows-amd64-windows-interrupt-precise-v1:966f5ed0034c4d3bd8ea5b4e1d37733f617d96363e154801722cc7e996fa6dd0:same-coordinate", "v2", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:e235c6f88ce85b5f9f58de08f96cad0949dd815f885d7c48e92e1deb39390343", "linux-arm64-linux-proc-boottime-v1:e235c6f88ce85b5f9f58de08f96cad0949dd815f885d7c48e92e1deb39390343:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:e235c6f88ce85b5f9f58de08f96cad0949dd815f885d7c48e92e1deb39390343", "linux-arm64-linux-proc-boottime-v1:e235c6f88ce85b5f9f58de08f96cad0949dd815f885d7c48e92e1deb39390343:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:0f71174bcf5117aac96dd8d72d65c8fe19400c8427207418ca6dd385f0399eef", "darwin-amd64-darwin-mach-continuous-v1:0f71174bcf5117aac96dd8d72d65c8fe19400c8427207418ca6dd385f0399eef:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:0f71174bcf5117aac96dd8d72d65c8fe19400c8427207418ca6dd385f0399eef", "darwin-amd64-darwin-mach-continuous-v1:0f71174bcf5117aac96dd8d72d65c8fe19400c8427207418ca6dd385f0399eef:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:ac25a482239171fb6c2f30ed7a99fde2dcbc5286ce843be85396b3e54c6059c4", "darwin-arm64-darwin-mach-continuous-v1:ac25a482239171fb6c2f30ed7a99fde2dcbc5286ce843be85396b3e54c6059c4:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:ac25a482239171fb6c2f30ed7a99fde2dcbc5286ce843be85396b3e54c6059c4", "darwin-arm64-darwin-mach-continuous-v1:ac25a482239171fb6c2f30ed7a99fde2dcbc5286ce843be85396b3e54c6059c4:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:8c2962978336831dc8b55e8f7d0522fc4d9f30f37d47d099930a08b1d5c7cd46", "windows-amd64-windows-interrupt-precise-v1:8c2962978336831dc8b55e8f7d0522fc4d9f30f37d47d099930a08b1d5c7cd46:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67", "linux-amd64-proc-boottime-v1:69c67733b7205b21652504146fe2e213681d86d90d0b5075f2ab7c1bf7990b67:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:8c2962978336831dc8b55e8f7d0522fc4d9f30f37d47d099930a08b1d5c7cd46", "windows-amd64-windows-interrupt-precise-v1:8c2962978336831dc8b55e8f7d0522fc4d9f30f37d47d099930a08b1d5c7cd46:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
}
