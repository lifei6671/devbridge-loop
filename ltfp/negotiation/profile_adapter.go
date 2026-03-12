package negotiation

import (
	stderrors "errors"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// EvaluateProfiles 使用 pb 协议对象执行协商并返回 pb 结果对象。
func EvaluateProfiles(local pb.NegotiationProfile, remote pb.NegotiationProfile) (pb.NegotiationResult, error) {
	result, err := Evaluate(
		Profile{
			VersionMajor:     local.VersionMajor,
			VersionMinor:     local.VersionMinor,
			RequiredFeatures: local.RequiredFeatures,
			OptionalFeatures: local.OptionalFeatures,
		},
		Profile{
			VersionMajor:     remote.VersionMajor,
			VersionMinor:     remote.VersionMinor,
			RequiredFeatures: remote.RequiredFeatures,
			OptionalFeatures: remote.OptionalFeatures,
		},
	)
	if err != nil {
		pbResult := pb.NegotiationResult{
			Accepted:           false,
			NegotiatedFeatures: nil,
			MissingRequired:    result.MissingRequired,
			ErrorMessage:       err.Error(),
		}
		var protocolErr *ltfperrors.ProtocolError
		// 错误链中包含协议错误时透传错误码，方便调用方分类处理。
		if stderrors.As(err, &protocolErr) {
			pbResult.ErrorCode = protocolErr.Code
		}
		return pbResult, err
	}
	return pb.NegotiationResult{
		Accepted:           result.Accepted,
		NegotiatedFeatures: result.NegotiatedFeatures,
		MissingRequired:    result.MissingRequired,
	}, nil
}
