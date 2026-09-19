package mediaruntime

import (
	"strings"
	"testing"
)

// FFmpeg and FFprobe wrap their license notice at a fixed column. The managed
// LGPL check must not depend on where that wrap happens.
func TestCollapseSpaceIgnoresLicenseWrapping(t *testing.T) {
	wrapped := "You should have received a copy of the GNU Lesser General Public\n" +
		"License along with ffmpeg; if not, write to the Free Software\n" +
		"Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA\n"
	if !strings.Contains(collapseSpace(wrapped), "GNU Lesser General Public License") {
		t.Fatal("collapsed license text lost the LGPL notice")
	}
	if strings.Contains(wrapped, "GNU Lesser General Public License") {
		t.Fatal("fixture is no longer wrapped; it would not prove the fix")
	}
}
