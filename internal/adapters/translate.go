package adapters

import (
	"github.com/ti-mo/conntrack"

	"survy/internal/ports"
)

// flowFromCT translates a ti-mo/conntrack Flow into a ports.Flow.
// CountersOrig are bytes sent by the originator (TupleOrig.Src);
// CountersReply are bytes sent back towards it.
func flowFromCT(f conntrack.Flow) ports.Flow {
	return ports.Flow{
		ID:         f.ID,
		Proto:      f.TupleOrig.Proto.Protocol,
		Orig:       tupleFromCT(f.TupleOrig),
		Reply:      tupleFromCT(f.TupleReply),
		OrigBytes:  f.CountersOrig.Bytes,
		ReplyBytes: f.CountersReply.Bytes,
	}
}

func tupleFromCT(t conntrack.Tuple) ports.Tuple {
	return ports.Tuple{
		Src:     t.IP.SourceAddress.Unmap(),
		Dst:     t.IP.DestinationAddress.Unmap(),
		SrcPort: t.Proto.SourcePort,
		DstPort: t.Proto.DestinationPort,
	}
}
