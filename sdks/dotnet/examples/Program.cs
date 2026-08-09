using System;
using System.Threading.Tasks;

namespace OpenRate.Examples
{
    /// <summary>
    /// Entry point for the two openrate examples.
    ///
    ///   dotnet run --project sdks/dotnet/examples -- direct
    ///   dotnet run --project sdks/dotnet/examples -- sidecar
    ///
    /// Prefer sdks/dotnet/run-examples.sh, which builds the shared library and
    /// the openrate binary first.
    /// </summary>
    internal static class Program
    {
        internal static async Task<int> Main(string[] args)
        {
            string which = args.Length > 0 ? args[0] : "both";
            if (which is not ("both" or "direct" or "sidecar"))
            {
                Console.Error.WriteLine($"unknown example: {which} (want: direct, sidecar, both)");
                return 2;
            }

            int status = 0;

            if (which is "both" or "direct")
            {
                Console.WriteLine("================ DirectRates (in-process, C ABI) ================");
                status |= await DirectRates.RunAsync();
                Console.WriteLine();
            }

            if (which is "both" or "sidecar")
            {
                Console.WriteLine("================ SidecarRates (child process, HTTP) =============");
                status |= await SidecarRates.RunAsync();
                Console.WriteLine();
            }

            return status;
        }
    }
}
