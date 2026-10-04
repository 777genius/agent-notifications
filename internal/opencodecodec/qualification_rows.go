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
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e733ebe1274bed49e09fa4e415373e0d5810312642c32bb697525de0a4906e73", "linux-amd64-proc-boottime-v1:e733ebe1274bed49e09fa4e415373e0d5810312642c32bb697525de0a4906e73:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:8efffcc38c10a17993dc3c534d044facda8845f41bf40dcfb62c99e013226d20", "linux-arm64-linux-proc-boottime-v1:8efffcc38c10a17993dc3c534d044facda8845f41bf40dcfb62c99e013226d20:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:195e663530bbab71a5b1f8a7d15c49b25e4d1bebb52b92e7df117f53b5e47a45", "linux-arm64-linux-proc-boottime-v1:195e663530bbab71a5b1f8a7d15c49b25e4d1bebb52b92e7df117f53b5e47a45:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:eac9edc9025c30e46c77e31c779c107a78ecc43e520e9bd8f748dd130e4dea0a", "darwin-amd64-darwin-mach-continuous-v1:eac9edc9025c30e46c77e31c779c107a78ecc43e520e9bd8f748dd130e4dea0a:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:7af860a797269f597bff3a0211f9b10647a67331b71286a8ed99a4ac55352411", "darwin-amd64-darwin-mach-continuous-v1:7af860a797269f597bff3a0211f9b10647a67331b71286a8ed99a4ac55352411:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:c737e3dc2dd81798539784409b0a4bdf7a8a1c651bae809813cf8d72f3de0330", "darwin-arm64-darwin-mach-continuous-v1:c737e3dc2dd81798539784409b0a4bdf7a8a1c651bae809813cf8d72f3de0330:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:0c9e79091099c6c69225d9e5a5d46289aafa172c511d0dc85e806523fc339b37", "darwin-arm64-darwin-mach-continuous-v1:0c9e79091099c6c69225d9e5a5d46289aafa172c511d0dc85e806523fc339b37:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:3e0face940d4ddd9d0f938c613ece92238c3e83b4df749aa6c34a0f790b2c770", "windows-amd64-windows-interrupt-precise-v1:3e0face940d4ddd9d0f938c613ece92238c3e83b4df749aa6c34a0f790b2c770:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, ClockRow{"bounded", "windows-amd64-windows-interrupt-precise-v1:c6a304d0e8e06f3ed201f39c34688f457bf3a17b792dfe114f767162626dc51f", "windows-amd64-windows-interrupt-precise-v1:c6a304d0e8e06f3ed201f39c34688f457bf3a17b792dfe114f767162626dc51f:same-coordinate", "v2", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:8efffcc38c10a17993dc3c534d044facda8845f41bf40dcfb62c99e013226d20", "linux-arm64-linux-proc-boottime-v1:8efffcc38c10a17993dc3c534d044facda8845f41bf40dcfb62c99e013226d20:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:8efffcc38c10a17993dc3c534d044facda8845f41bf40dcfb62c99e013226d20", "linux-arm64-linux-proc-boottime-v1:8efffcc38c10a17993dc3c534d044facda8845f41bf40dcfb62c99e013226d20:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:eac9edc9025c30e46c77e31c779c107a78ecc43e520e9bd8f748dd130e4dea0a", "darwin-amd64-darwin-mach-continuous-v1:eac9edc9025c30e46c77e31c779c107a78ecc43e520e9bd8f748dd130e4dea0a:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:eac9edc9025c30e46c77e31c779c107a78ecc43e520e9bd8f748dd130e4dea0a", "darwin-amd64-darwin-mach-continuous-v1:eac9edc9025c30e46c77e31c779c107a78ecc43e520e9bd8f748dd130e4dea0a:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:c737e3dc2dd81798539784409b0a4bdf7a8a1c651bae809813cf8d72f3de0330", "darwin-arm64-darwin-mach-continuous-v1:c737e3dc2dd81798539784409b0a4bdf7a8a1c651bae809813cf8d72f3de0330:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:c737e3dc2dd81798539784409b0a4bdf7a8a1c651bae809813cf8d72f3de0330", "darwin-arm64-darwin-mach-continuous-v1:c737e3dc2dd81798539784409b0a4bdf7a8a1c651bae809813cf8d72f3de0330:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:3e0face940d4ddd9d0f938c613ece92238c3e83b4df749aa6c34a0f790b2c770", "windows-amd64-windows-interrupt-precise-v1:3e0face940d4ddd9d0f938c613ece92238c3e83b4df749aa6c34a0f790b2c770:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a", "linux-amd64-proc-boottime-v1:c534e26d92f7f3f126553460cce983c6962135d9c40960c483fb7ce24d4eb55a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:3e0face940d4ddd9d0f938c613ece92238c3e83b4df749aa6c34a0f790b2c770", "windows-amd64-windows-interrupt-precise-v1:3e0face940d4ddd9d0f938c613ece92238c3e83b4df749aa6c34a0f790b2c770:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
}
