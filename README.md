# conb
動画出力に関するrepository

外部ライブラリを使わず、Go で画面描画の仕組みを作るプロジェクトです。

## Canvas

`Canvas` は 1 ピクセルを R、G、B、A 各 8 bit（計 4 byte）で保持します。
座標の原点は左上で、X は右方向、Y は下方向に増加します。

```go
canvas, err := conb.NewCanvas(640, 480)
if err != nil {
    panic(err)
}

canvas.Clear(conb.Color{R: 20, G: 20, B: 30, A: 255})
canvas.SetPixel(100, 50, conb.Color{R: 255, A: 255})
```

## OS ごとの画面表示

アプリケーションは共通の `Display` interface だけを使います。
OS ごとの型はこの interface のメソッドを実装します。
Go では明示的な継承宣言は不要です。

```go
type windowsDisplay struct {
    // HWND など、Windows 固有の状態
}

// 全メソッドの実装漏れをコンパイル時に検出します。
var _ conb.Display = (*windowsDisplay)(nil)
```

今後のファイル構成は次のようにします。

```text
canvas.go           ピクセル操作（OS 非依存）
display.go          Display interface（OS 非依存）
display_windows.go  Win32 API による実装（//go:build windows）
display_linux.go    Linux 用の実装（//go:build linux）
```

アプリケーションの描画ループは `Present` で Canvas を転送し、`PollEvents` でOSのイベントを処理します。

## Windows デモ

Windows実装はGoの標準ライブラリからWin32 APIを直接呼び出します。
外部ライブラリやCGOは使用しません。

```powershell
go run ./cmd/demo
```

## Linux (X11)

Linuxでは `DISPLAY` が示すX11サーバーへUnixソケットまたはTCPで直接接続します。
XlibやCGOは使用せず、認証が必要な場合は `XAUTHORITY` または `~/.Xauthority` のMIT-MAGIC-COOKIEを読み込みます。

```sh
go run ./cmd/demo
```

XWaylandが動いているWayland環境でも、`DISPLAY` が設定されていれば利用できます。
WindowsとLinux以外では、`NewDisplay` は `ErrDisplayUnsupported` を返します。

## MP4

`convert/mp4` は外部ライブラリなしのMP4コンテナパーサーです。
大きな `mdat` をメモリへ丸ごと読み込まず、`io.ReaderAt` を使って必要な範囲だけ参照します。

```go
source, err := os.Open("movie.mp4")
if err != nil {
    panic(err)
}
defer source.Close()

info, err := source.Stat()
if err != nil {
    panic(err)
}
container, err := mp4.Parse(source, info.Size())
if err != nil {
    panic(err)
}
movie, err := container.Movie()
```

現在はBox、ファイル種別、時間軸、トラック、動画解像度、サンプル記述、`avcC` などのデコーダー設定を解析できます。
さらに `stts`、`ctts`、`stsc`、`stsz`、`stco/co64`、`stss` から圧縮サンプルの位置、DTS、PTS、キーフレーム情報を構築します。

```go
for i := 0; i < videoTrack.Samples.Len(); i++ {
    metadata, _ := videoTrack.Samples.Sample(i)
    compressedFrame, err := videoTrack.Samples.Read(i)
    // compressedFrameを、SampleEntryに対応するデコーダーへ渡します。
    _ = metadata
    _ = compressedFrame
    _ = err
}
```

この圧縮サンプルと `avcC` は、次のH.264パッケージへ直接渡せます。

## H.264

`convert/h264` はMP4コンテナから独立したH.264処理パッケージです。
`avcC`、MP4形式の長さ付きNAL unit、Annex B、RBSPのemulation-prevention byte、Exp-Golomb、SPS/PPSの基礎情報を解析します。

```go
entry := videoTrack.SampleEntries[0]
config, err := h264.ParseAVCConfig(entry.DecoderConfig)
if err != nil {
    panic(err)
}

spsNAL, err := h264.ParseNALUnit(config.SequenceHeaders[0])
sps, err := h264.ParseSPS(spsNAL)

compressedSample, err := videoTrack.Samples.Read(0)
units, err := h264.ParseSample(compressedSample, config.NALLengthSize)
```

SPSから符号化サイズとクロップ後の表示サイズも取得でき、parameter-set IDもsliceごとに解決できます。

```go
store, err := h264.StoreFromConfig(config)
for _, unit := range units {
    if unit.Type == h264.NALSliceIDR || unit.Type == h264.NALSliceNonIDR {
        header, err := h264.ParseSliceHeader(unit, store)
        _ = header
        _ = err
    }
}
```

slice parserは共通prefix、frame/field、IDR、POC、reference list、prediction weight、reference marking/MMCO、CABAC初期値、slice QP/QS、deblocking設定までを解析します。
`SliceDataReader` は解析済みheaderの直後に位置するRBSP readerを返します。
複数slice group（FMO）は現在未対応として明示的にエラーを返します。

CAVLCでは `0 <= nC` と2x2 chroma DCの `coeff_token`、trailing-one sign、level prefix/suffix、`total_zeros`、`run_before`、スキャン順への係数配置を独自実装しています。
`DecodeResidualBlockCAVLC` で4係数または16係数の残差ブロックを復号できます。
4:2:2で使う2x4 chroma DC（`nC=-2`）は現在未対応です。

I-sliceでは `I_NxN` / `I_16x16` のmacroblock type、イントラ予測モード、coded block pattern、QP deltaを解析できます。
`CAVLCBlockContext` が左・上の4x4ブロックから `nC` を導出し、`DecodeIntra4x4LumaResidual` が16個のluma残差ブロックへCAVLCを接続します。
残差の逆量子化・整数逆変換、Intra4x4 / Intra16x16 / 4:2:0 chroma予測、YUV420からCanvasへのBT.601変換まで接続済みです。

## MP4プレイヤー

コンテナ、H.264復号、Canvas、OS固有Displayをつないだ実行コマンドです。

```sh
go run ./cmd/play movie.mp4
```

現在の独自デコーダーが受理する映像はprogressive・8-bit・YUV 4:2:0・CAVLCのI-slice（Intra4x4 / Intra16x16 / I_PCM）と、P_Skip、P_L0_16x16、P_16x8、P_8x16、P_8x8（8x8 / 8x4 / 4x8 / 4x4 sub-partition）のP-sliceです。
P-picture内のIntraマクロブロックと明示的weighted predictionにも対応します。
P_L0ではquarter-pel luma 6-tap補間、1/8-pel chroma双線形補間、CAVLC残差を再構成します。
I-pictureには規定の境界強度3/4によるインループ・デブロッキングを適用します。
P/B-pictureでもIntra、非ゼロ係数、参照画像、動きベクトルから境界強度0〜4を導出します。
SPSの表示クロップもCanvasへ反映します。
DPBはshort/long-term picture、sliding-window、MMCO 1〜6、参照リスト変更を管理し、partitionごとの複数参照画像を選択できます。
B-sliceではL0 / L1 / Biの16x16、16x8、8x16と、非Direct B_8x8 sub-partition、および明示weighted bipredictionに対応します。
B Direct/Skipでは復号画像に保持したcolocated motion fieldからspatial / temporal Directを導出し、implicit weighted bipredictionにも対応します。
CABACはrange/offset算術復号、LPS range表、MPS/LPS状態遷移、bypass、terminate、context初期状態のコアに加え、`mb_qp_delta`、`intra_chroma_pred_mode`、Intra4x4予測モード、`ref_idx_lX`、coded block pattern、I/P macroblock typeのbinarizationとcontext選択まで実装済みです。
B macroblock type、P/B sub-macroblock type、`mvd_lX`のUEG3 prefix・bypass suffix・符号bitにも対応しています。
I/SI sliceについてはframe-coded residual用context 105〜275を初期化し、`coded_block_flag`、significance map、`last_significant_coeff_flag`、逆順level、bypass-coded level suffixと符号bitをblock係数列まで復号できます。
4x4 luma、Intra16x16 luma DC/AC、4:2:0 chroma DC/ACの既存変換入力形式へも接続済みです。
P/B sliceでも`cabac_init_idc=0/1/2`すべてについて、progressive 4x4 residual用context 105〜275を初期化します。
field-coded residual用context 277〜398は、現在のprogressive限定profileでは使用しません。
slice dataにはI_4x4 / I_16x16 / I_PCMを混在できるCABAC I-pictureを接続しています。
CABAC P-pictureはP_Skip、P_L0_16x16、P_16x8、P_8x16、P_8x8を混在でき、P_8x8内の8x8 / 8x4 / 4x8 / 4x4 sub-partitionを展開します。
partitionごとの複数参照、MVD、weighted prediction、CBP、QP差分、luma/chroma residualまで再構成します。
CABAC B-pictureは現在、B_Skip / Direct、L0 / L1 / Biの16x16・16x8・8x16、B_8x8内のDirect / L0 / L1 / Bi sub-partition、およびI_4x4 / I_16x16 / I_PCMを混在でき、複数参照、list別MVD、explicit / implicit weighted biprediction、CABAC residualまで再構成します。
現在の対応範囲はprogressive、8-bit、4:2:0、4x4 transformです。
interlaced/field coding、4:2:2、4:4:4、High profileの8x8 transformは未対応で、該当データでは明示的なエラーを返します。
外部コーデックへフォールバックはしません。

プレイヤーはsampleをDTS順に復号しながら、`ctts`のcomposition timeで導出したPTS順に並べ替えて表示します。
B-frameの並べ替え中だけ復号済みframeを保持し、表示後に解放します。

テストでは最小のavc1 MP4をバイト列から構築し、`mdat`のサンプル読み出し、IDR復号、YUVからCanvasへの変換までを一つの統合テストで検証しています。
さらに実際に符号化された12-frameのCAVLC/CABAC MP4をBase64テストデータとして同梱し、外部コマンドや外部デコーダーを呼ばずに全sampleの復号とY-plane checksumを検証します。

```sh
go test ./...
go vet ./...
GOOS=windows GOARCH=amd64 go build ./cmd/play
GOOS=linux GOARCH=amd64 go build ./cmd/play
```
