# Make the CUDA SDK shipped by DGX OS available in authenticated Cloudless
# terminal sessions. Keep this conditional so generic CloudlessOS machines
# without a CUDA toolkit retain their normal environment.
if [ -x /usr/local/cuda/bin/nvcc ]; then
    export CUDA_HOME=/usr/local/cuda
    case ":$PATH:" in
        *:/usr/local/cuda/bin:*) ;;
        *) export PATH="/usr/local/cuda/bin:$PATH" ;;
    esac
fi
